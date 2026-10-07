package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// fakeAuthority stands in for Identity: bundles per admin and one-time
// proofs bound to user, purpose and operation.
type fakeAuthority struct {
	mu     sync.Mutex
	grants map[string][]string
	proofs map[string][3]string
}

func (f *fakeAuthority) Require(_ context.Context, user, permission string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if slices.Contains(f.grants[user], permission) {
		return 3, nil
	}
	return 0, adminaccess.Missing(permission)
}

func (f *fakeAuthority) proof(user, purpose, op string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := uuid.NewString()
	f.proofs[p] = [3]string{user, purpose, op}
	return p
}

func (f *fakeAuthority) ConsumeProof(_ context.Context, proof, user, purpose, op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if want, ok := f.proofs[proof]; !ok || want != [3]string{user, purpose, op} {
		return adminaccess.ReauthRequired()
	}
	delete(f.proofs, proof)
	return nil
}

func appCode(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func TestManualMoneyActionsNeedTwoAdmins(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	maker, checkerA, checkerB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := &fakeAuthority{proofs: map[string][3]string{}, grants: map[string][]string{
		maker: {adminaccess.FinancePrepare}, checkerA: {adminaccess.FinanceApprove}, checkerB: {adminaccess.FinanceApprove}}}
	e.refunds.RequireApprovals(true)
	e.settle.RequireApprovals = true
	now := time.Now().UTC()
	tx := repository.Transactions{Pool: e.pool}
	uc := &usecase.ApprovalUseCase{Store: repository.ApprovalRepository{Pool: e.pool}, Refunds: e.refunds, Settlement: e.settle,
		Payouts: repository.NewPayoutRepository(e.pool), RefundsRepo: repository.NewRefundRepository(e.pool), Audit: repository.NewAuditRepository(e.pool),
		Tx: tx, Admins: auth, Enabled: true, Now: func() time.Time { return now }, Log: zerolog.Nop()}

	order := uuid.NewString()
	capturedIntent(t, e.pool, order, 5000)
	req := refundReq(order, 1200)
	refund, _, err := repository.NewRefundRepository(e.pool).Request(ctx, req, check(req))
	if err != nil {
		t.Fatal(err)
	}
	resolution := domain.RefundResolution{Outcome: domain.RefundSucceeded, EvidenceReference: "FAKE-BANK-REF-1"}
	if _, err := e.refunds.Resolve(ctx, maker, refund.ID, resolution); appCode(err) != "approval_required" {
		t.Fatalf("direct resolution must need approval: %v", err)
	}
	if _, err := e.settle.Adjust(ctx, maker, uuid.NewString(), 100, "VND", "Test adjustment"); appCode(err) != "approval_required" {
		t.Fatalf("direct adjustment must need approval: %v", err)
	}

	payload := json.RawMessage(`{"outcome":"succeeded","evidence_reference":" FAKE-BANK-REF-1 "}`)
	draft, err := uc.Draft(ctx, maker, usecase.DraftInput{Kind: domain.ApprovalRefundResolution, TargetID: refund.ID, Payload: payload, Reason: "Bank confirmed", PermissionVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Draft(ctx, maker, usecase.DraftInput{Kind: domain.ApprovalRefundResolution, TargetID: refund.ID, Payload: payload, Reason: "Again"}); appCode(err) != "approval_open" {
		t.Fatalf("second open request for a target: %v", err)
	}
	if _, err := uc.Submit(ctx, maker, draft.ID, auth.proof(maker, domain.ProofPurposeSubmit, "another-operation"), draft.Version); appCode(err) != adminaccess.CodeReauthRequired {
		t.Fatalf("proof for another operation accepted: %v", err)
	}
	pending, err := uc.Submit(ctx, maker, draft.ID, auth.proof(maker, domain.ProofPurposeSubmit, draft.PayloadHash), draft.Version)
	if err != nil || pending.Status != domain.ApprovalPending {
		t.Fatalf("submit failed: %v", err)
	}
	if _, err := uc.Decide(ctx, maker, draft.ID, usecase.DecisionInput{Approve: true, Proof: auth.proof(maker, domain.ProofPurposeDecide, draft.PayloadHash),
		ExpectedVersion: pending.Version, Reason: "Self"}); appCode(err) != "self_approval" {
		t.Fatalf("maker approved its own request: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE approval_requests SET payload = '{"outcome":"failed"}' WHERE id = $1`, draft.ID); err == nil {
		t.Fatal("request payload changed after creation")
	}

	// Two checkers approve the same version at once: one executes.
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, c := range []string{checkerA, checkerB} {
		p := auth.proof(c, domain.ProofPurposeDecide, draft.PayloadHash)
		go func() {
			<-start
			_, err := uc.Decide(context.WithoutCancel(ctx), c, draft.ID, usecase.DecisionInput{Approve: true, Proof: p, ExpectedVersion: pending.Version, Reason: "Evidence checked"})
			results <- err
		}()
	}
	close(start)
	ok := 0
	for range 2 {
		if <-results == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("expected one approval, got %d", ok)
	}
	var status, executed string
	if err := e.pool.QueryRow(ctx, `SELECT r.status, a.execution_ref FROM payment_refunds r, approval_requests a WHERE r.id=$1 AND a.id=$2`, refund.ID, draft.ID).Scan(&status, &executed); err != nil {
		t.Fatal(err)
	}
	if status != string(domain.RefundSucceeded) || executed != "refund:"+refund.ID {
		t.Fatalf("approval did not execute once: %s %s", status, executed)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE target_id=$1 AND action IN ('approval_drafted','approval_submitted','approval_approved')`, draft.ID); n != 3 {
		t.Fatalf("approval steps not audited: %d", n)
	}

	// The maker loses finance.prepare before the decision: refused.
	vendor := uuid.NewString()
	adj, err := uc.Draft(ctx, maker, usecase.DraftInput{Kind: domain.ApprovalSettlementAdjustment, TargetID: vendor,
		Payload: json.RawMessage(`{"amount":-500,"currency":"vnd","reason":"Test write-off"}`), Reason: "Debt written off"})
	if err != nil {
		t.Fatal(err)
	}
	adj, err = uc.Submit(ctx, maker, adj.ID, auth.proof(maker, domain.ProofPurposeSubmit, adj.PayloadHash), adj.Version)
	if err != nil {
		t.Fatal(err)
	}
	auth.grants[maker] = nil
	if _, err := uc.Decide(ctx, checkerA, adj.ID, usecase.DecisionInput{Approve: true, Proof: auth.proof(checkerA, domain.ProofPurposeDecide, adj.PayloadHash),
		ExpectedVersion: adj.Version, Reason: "Checked"}); appCode(err) != "maker_permission_revoked" {
		t.Fatalf("approval after the maker lost its permission: %v", err)
	}
	auth.grants[maker] = []string{adminaccess.FinancePrepare}
	// A day later the request has expired and stays expired.
	now = now.Add(domain.ApprovalTTL + time.Minute)
	if _, err := uc.Decide(ctx, checkerA, adj.ID, usecase.DecisionInput{Approve: true, Proof: auth.proof(checkerA, domain.ProofPurposeDecide, adj.PayloadHash),
		ExpectedVersion: adj.Version, Reason: "Late"}); appCode(err) != "approval_expired" {
		t.Fatalf("expired request approved: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM approval_requests WHERE id=$1 AND status='expired'`, adj.ID); n != 1 {
		t.Fatal("expiry not recorded")
	}
	if n := e.count(t, `SELECT count(*) FROM settlement_entries WHERE vendor_id=$1`, vendor); n != 0 {
		t.Fatal("expired adjustment posted")
	}

	// The target changes after preparation: approval refused, nothing paid.
	order2 := uuid.NewString()
	capturedIntent(t, e.pool, order2, 5000)
	req2 := refundReq(order2, 700)
	refund2, _, err := repository.NewRefundRepository(e.pool).Request(ctx, req2, check(req2))
	if err != nil {
		t.Fatal(err)
	}
	d2, err := uc.Draft(ctx, maker, usecase.DraftInput{Kind: domain.ApprovalRefundResolution, TargetID: refund2.ID, Payload: payload, Reason: "Bank confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	d2, err = uc.Submit(ctx, maker, d2.ID, auth.proof(maker, domain.ProofPurposeSubmit, d2.PayloadHash), d2.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payment_refunds SET status='pending' WHERE id=$1`, refund2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Decide(ctx, checkerB, d2.ID, usecase.DecisionInput{Approve: true, Proof: auth.proof(checkerB, domain.ProofPurposeDecide, d2.PayloadHash),
		ExpectedVersion: d2.Version, Reason: "Checked"}); appCode(err) != "stale_snapshot" {
		t.Fatalf("changed target approved: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM payment_refunds WHERE id=$1 AND status='pending'`, refund2.ID); n != 1 {
		t.Fatal("stale approval resolved the refund")
	}
	rejected, err := uc.Decide(ctx, checkerB, d2.ID, usecase.DecisionInput{Approve: false, Proof: auth.proof(checkerB, domain.ProofPurposeDecide, d2.PayloadHash),
		ExpectedVersion: d2.Version, Reason: "Target changed"})
	if err != nil || rejected.Status != domain.ApprovalRejected || rejected.ExecutionRef != nil {
		t.Fatalf("rejection failed: %v", err)
	}
}
