package repository_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// memoryEvidence stands in for the private bucket.
type memoryEvidence struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memoryEvidence) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memoryEvidence) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(m.objects[key])), nil
}

func (m *memoryEvidence) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

type manualFixture struct {
	e                                         *env
	uc                                        *usecase.ManualRefundUseCase
	auth                                      *fakeAuthority
	clock                                     atomic.Pointer[time.Time]
	preparer, preparer2, approverA, approverB string
}

func newManualFixture(t *testing.T) *manualFixture {
	t.Helper()
	f := &manualFixture{e: newEnv(t), preparer: uuid.NewString(), preparer2: uuid.NewString(), approverA: uuid.NewString(), approverB: uuid.NewString()}
	f.auth = &fakeAuthority{proofs: map[string][3]string{}, grants: map[string][]string{
		f.preparer: {adminaccess.FinancePrepare}, f.preparer2: {adminaccess.FinancePrepare},
		f.approverA: {adminaccess.FinanceApprove}, f.approverB: {adminaccess.FinanceApprove}}}
	now := time.Now().UTC()
	f.clock.Store(&now)
	cipher, err := adapter.NewDestinationCipher(1, map[int][]byte{1: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	f.uc = &usecase.ManualRefundUseCase{Store: repository.ManualRefundRepository{Pool: f.e.pool}, Refunds: f.e.refunds,
		Audit: repository.NewAuditRepository(f.e.pool), Tx: repository.Transactions{Pool: f.e.pool}, Admins: f.auth, Cipher: cipher,
		Evidence: &memoryEvidence{objects: map[string][]byte{}}, Enabled: true, StepUp: true, TwoPerson: true, Lease: 30 * time.Minute,
		Now: func() time.Time { return *f.clock.Load() }, Log: zerolog.Nop()}
	f.e.refunds.WithLegacyGuard(f.uc.GuardLegacyResolution)
	return f
}

func (f *manualFixture) advance(d time.Duration) {
	next := f.clock.Load().Add(d)
	f.clock.Store(&next)
}

// refundFor creates a captured payment of buyer and a refund on it.
func (f *manualFixture) refundFor(t *testing.T, buyer string, amount int64) *domain.Refund {
	t.Helper()
	order, intent := uuid.NewString(), uuid.NewString()
	if _, err := f.e.pool.Exec(t.Context(), `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id)
		VALUES($1,$2,$3,$4,'VND','captured','test',$5)`, intent, order, buyer, amount*2, "fake-ref-"+intent); err != nil {
		t.Fatal(err)
	}
	r, _, err := f.e.refunds.Request(t.Context(), refundReq(order, amount))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var fakeBeneficiary = domain.Beneficiary{BankCode: "FAKEBANK", AccountNumber: "9704000011112222", AccountName: "Nguyen Van Test"}

// verified gives the refund a verified destination and returns its version.
func (f *manualFixture) verified(t *testing.T, buyer, refundID string) int {
	t.Helper()
	ctx := t.Context()
	v, err := f.uc.SubmitDestination(ctx, buyer, refundID, fakeBeneficiary, 0)
	if err != nil {
		t.Fatal(err)
	}
	version := v.Destination.Version
	proof := f.auth.proof(f.approverA, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(refundID, version, true))
	if _, err := f.uc.DecideDestination(ctx, f.approverA, refundID, usecase.DestinationDecision{Version: version, Verify: true, Reason: "Name matches the order", Proof: proof}); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestManualRefundNeedsAVerifiedDestinationAndASecondAdmin(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	buyer, stranger := uuid.NewString(), uuid.NewString()
	refund := f.refundFor(t, buyer, 1200)

	if _, err := f.uc.SubmitDestination(ctx, stranger, refund.ID, fakeBeneficiary, 0); appCode(err) != "not_found" {
		t.Fatalf("another buyer must not see the refund: %v", err)
	}
	if _, err := f.uc.BuyerRefund(ctx, stranger, refund.ID); appCode(err) != "not_found" {
		t.Fatalf("another buyer must not read the refund: %v", err)
	}
	if _, err := f.uc.SubmitDestination(ctx, buyer, refund.ID, fakeBeneficiary, 1); appCode(err) != "destination_changed" {
		t.Fatalf("a stale expected version must be refused: %v", err)
	}
	view, err := f.uc.SubmitDestination(ctx, buyer, refund.ID, fakeBeneficiary, 0)
	if err != nil || view.Destination.Version != 1 || view.BuyerStage != "verifying" || view.Destination.Masked() != "FAKEBANK ••••2222" {
		t.Fatalf("submit destination: %+v %v", view, err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM refund_destinations WHERE position(convert_to('9704000011112222', 'UTF8') in ciphertext) > 0`); n != 0 {
		t.Fatal("the account number must only be stored encrypted")
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE reason LIKE '%9704000011112222%' OR reason LIKE '%NGUYEN VAN TEST%'`); n != 0 {
		t.Fatal("the audit must not contain the account")
	}

	direct := domain.RefundResolution{Outcome: domain.RefundSucceeded, EvidenceReference: "FAKE-REF"}
	if _, err := f.e.refunds.Resolve(ctx, f.preparer, refund.ID, direct); appCode(err) != "manual_workflow_required" {
		t.Fatalf("the one-step success must go through the workflow: %v", err)
	}
	if _, err := f.uc.PrepareAttempt(ctx, f.preparer, refund.ID, 1, "Transfer refund"); appCode(err) != "destination_not_verified" {
		t.Fatalf("an unverified destination cannot be paid: %v", err)
	}
	if _, err := f.uc.DecideDestination(ctx, f.approverA, refund.ID, usecase.DestinationDecision{Version: 1, Verify: true, Reason: "ok"}); appCode(err) != adminaccess.CodeReauthRequired {
		t.Fatalf("verifying needs a password proof: %v", err)
	}
	proof := f.auth.proof(f.approverA, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(refund.ID, 1, true))
	if _, err := f.uc.DecideDestination(ctx, f.approverA, refund.ID, usecase.DestinationDecision{Version: 1, Verify: true, Reason: "Name matches", Proof: proof}); err != nil {
		t.Fatal(err)
	}

	// Only one of several concurrent preparations opens an attempt.
	var wg sync.WaitGroup
	var opened atomic.Int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.uc.PrepareAttempt(context.Background(), f.preparer, refund.ID, 1, "Transfer refund"); err == nil {
				opened.Add(1)
			} else if appCode(err) != "attempt_active" {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if opened.Load() != 1 {
		t.Fatalf("exactly one attempt must open, got %d", opened.Load())
	}
	// Rolling the flag back never reopens the one-step path around it.
	f.uc.Enabled = false
	if _, err := f.e.refunds.Resolve(ctx, f.preparer, refund.ID, domain.RefundResolution{Outcome: domain.RefundFailed, Note: "x"}); appCode(err) != "attempt_active" {
		t.Fatalf("an open attempt blocks the one-step resolution: %v", err)
	}
	f.uc.Enabled = true

	// The buyer changing the destination voids the unsent attempt.
	view, err = f.uc.SubmitDestination(ctx, buyer, refund.ID, domain.Beneficiary{BankCode: "FAKEBANK", AccountNumber: "9704000033334444", AccountName: "Nguyen Van Test"}, 1)
	if err != nil || view.Destination.Version != 2 || view.Attempt != nil {
		t.Fatalf("change destination: %+v %v", view, err)
	}
	proof = f.auth.proof(f.approverA, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(refund.ID, 2, true))
	if _, err := f.uc.DecideDestination(ctx, f.approverA, refund.ID, usecase.DestinationDecision{Version: 2, Verify: true, Reason: "ok", Proof: proof}); err != nil {
		t.Fatal(err)
	}
	attempt, err := f.uc.PrepareAttempt(ctx, f.preparer, refund.ID, 2, "Transfer refund")
	if err != nil || attempt.Amount != 1200 || attempt.Currency != "VND" {
		t.Fatalf("prepare: %+v %v", attempt, err)
	}

	// Two operators claim at once: one transfers.
	var claimed atomic.Int32
	for _, op := range []string{f.preparer, f.preparer2} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			if _, err := f.uc.Claim(context.Background(), op, attempt.ID, attempt.Version); err == nil {
				claimed.Add(1)
			}
		}(op)
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("exactly one claim must win, got %d", claimed.Load())
	}
	detail, err := f.uc.Detail(ctx, refund.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := detail.View.Attempt
	claimer, other := *current.ClaimedBy, f.preparer
	if claimer == f.preparer {
		other = f.preparer2
	}

	if _, err := f.uc.RevealDestination(ctx, other, refund.ID, "curious", ""); appCode(err) != adminaccess.CodeMissingPermission {
		t.Fatalf("only the claimer or a verifier reads the account: %v", err)
	}
	proof = f.auth.proof(claimer, domain.ProofPurposeDestinationReveal, domain.DestinationRevealRef(refund.ID, 2))
	revealed, err := f.uc.RevealDestination(ctx, claimer, refund.ID, "Making the transfer", proof)
	if err != nil || revealed.Beneficiary.AccountNumber != "9704000033334444" || revealed.Beneficiary.AccountName != "NGUYEN VAN TEST" {
		t.Fatalf("reveal: %+v %v", revealed, err)
	}
	if _, err := f.uc.SubmitDestination(ctx, buyer, refund.ID, fakeBeneficiary, 2); appCode(err) != "attempt_active" {
		t.Fatalf("the destination cannot change while money may be moving: %v", err)
	}

	evidence, err := f.uc.UploadEvidence(ctx, claimer, current.ID, []byte("\x89PNG\r\n\x1a\nfake receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UploadEvidence(ctx, other, current.ID, []byte("%PDF-1.4 fake")); appCode(err) != "not_claim_owner" {
		t.Fatalf("only the claimer adds evidence: %v", err)
	}
	submitted, err := f.uc.Submit(ctx, claimer, current.ID, usecase.SubmitInput{ExpectedVersion: current.Version, EvidenceIDs: []string{evidence.ID},
		Submission: domain.Submission{BankReference: "FAKE-FT-0001", SourceAccount: "PLATFORM-FAKEBANK", ExecutedAt: f.clock.Load().Add(-time.Minute)}})
	if err != nil || submitted.Stage != domain.AttemptSubmitted {
		t.Fatalf("submit: %+v %v", submitted, err)
	}

	confirm := func(actor string, version int) error {
		proof := f.auth.proof(actor, domain.ProofPurposeAttemptDecide, domain.AttemptDecisionRef(current.ID, version, true))
		_, err := f.uc.Decide(ctx, actor, current.ID, usecase.AttemptDecision{ExpectedVersion: version, Confirm: true, Reason: "Statement shows the transfer", Proof: proof})
		return err
	}
	if err := confirm(claimer, submitted.Version); appCode(err) != "self_approval" {
		t.Fatalf("the executor cannot confirm: %v", err)
	}
	wrongOp := f.auth.proof(f.approverB, domain.ProofPurposeAttemptDecide, domain.AttemptDecisionRef(current.ID, submitted.Version, false))
	if _, err := f.uc.Decide(ctx, f.approverB, current.ID, usecase.AttemptDecision{ExpectedVersion: submitted.Version, Confirm: true, Reason: "x", Proof: wrongOp}); appCode(err) != adminaccess.CodeReauthRequired {
		t.Fatalf("a proof for another decision must not work: %v", err)
	}
	if err := confirm(f.approverB, submitted.Version); err != nil {
		t.Fatal(err)
	}
	if err := confirm(f.approverA, submitted.Version); err == nil {
		t.Fatal("a confirmed transfer cannot be confirmed again")
	}

	done, err := f.uc.BuyerRefund(ctx, buyer, refund.ID)
	if err != nil || done.Refund.Status != domain.RefundSucceeded || done.BuyerStage != "refunded" ||
		*done.Refund.EvidenceReference != "bank:PLATFORM-FAKEBANK:FAKE-FT-0001" {
		t.Fatalf("refund after confirmation: %+v %v", done, err)
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_refund_sync WHERE payment_refund_id = $1`, refund.ID); n != 1 {
		t.Fatal("the outcome must be queued for Order in the confirmation's transaction")
	}
	if n := f.e.count(t, `SELECT count(*) FROM refund_evidence WHERE id = $1 AND state = 'attached'`, evidence.ID); n != 1 {
		t.Fatal("submitted evidence is attached")
	}
	if n := f.e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE target_id = $1 AND action IN
		('refund_destination_submitted','refund_destination_verified','manual_refund_attempt_prepared','manual_refund_attempt_voided',
		 'manual_refund_attempt_executing','refund_destination_revealed','manual_refund_attempt_submitted','manual_refund_attempt_confirmed')`, refund.ID); n < 9 {
		t.Fatalf("every step is audited, got %d rows", n)
	}
}

func TestExpiredClaimIsUnknownUntilTheStatementIsChecked(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	refund := f.refundFor(t, buyer, 800)
	version := f.verified(t, buyer, refund.ID)
	attempt, err := f.uc.PrepareAttempt(ctx, f.preparer, refund.ID, version, "Transfer refund")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = f.uc.Claim(ctx, f.preparer, attempt.ID, attempt.Version)
	if err != nil {
		t.Fatal(err)
	}
	f.advance(31 * time.Minute)
	if moved, err := f.uc.ExpireClaims(ctx, 10); err != nil || moved != 1 {
		t.Fatalf("expire: %d %v", moved, err)
	}
	if _, err := f.uc.Cancel(ctx, f.preparer, attempt.ID, attempt.Version+1, "nothing sent"); appCode(err) != "stale_attempt" {
		t.Fatalf("an unknown transfer cannot be voided: %v", err)
	}
	if _, err := f.uc.PrepareAttempt(ctx, f.preparer2, refund.ID, version, "Try again"); appCode(err) != "attempt_active" {
		t.Fatalf("no second transfer while one may have left: %v", err)
	}
	failProof := f.auth.proof(f.approverA, domain.ProofPurposeAttemptDecide, domain.AttemptDecisionRef(attempt.ID, attempt.Version+1, false))
	failed, err := f.uc.Decide(ctx, f.approverA, attempt.ID, usecase.AttemptDecision{ExpectedVersion: attempt.Version + 1, Reason: "Not on the statement", Proof: failProof})
	if err != nil || failed.Stage != domain.AttemptFailed {
		t.Fatalf("fail unknown: %+v %v", failed, err)
	}
	if r, _ := f.uc.BuyerRefund(ctx, buyer, refund.ID); r.Refund.Status != domain.RefundAwaitingProvider {
		t.Fatal("a failed transfer leaves the refund open")
	}

	// A bank reference proves one transfer: reusing it is refused.
	submit := func(refundID string, version int, ref string) error {
		a, err := f.uc.PrepareAttempt(ctx, f.preparer, refundID, version, "Transfer refund")
		if err != nil {
			return err
		}
		a, err = f.uc.Claim(ctx, f.preparer, a.ID, a.Version)
		if err != nil {
			return err
		}
		_, err = f.uc.Submit(ctx, f.preparer, a.ID, usecase.SubmitInput{ExpectedVersion: a.Version,
			Submission: domain.Submission{BankReference: ref, SourceAccount: "PLATFORM-FAKEBANK", ExecutedAt: *f.clock.Load()}})
		return err
	}
	if err := submit(refund.ID, version, "FAKE-FT-0002"); err != nil {
		t.Fatal(err)
	}
	other := f.refundFor(t, uuid.NewString(), 300)
	otherVersion := f.verified(t, mustBuyer(t, f, other.ID), other.ID)
	if err := submit(other.ID, otherVersion, "fake ft 0002"); appCode(err) != "duplicate_bank_reference" {
		t.Fatalf("the same reference for another transfer must be refused: %v", err)
	}
}

func mustBuyer(t *testing.T, f *manualFixture, refundID string) string {
	t.Helper()
	buyer, err := repository.ManualRefundRepository{Pool: f.e.pool}.RefundBuyer(t.Context(), refundID)
	if err != nil {
		t.Fatal(err)
	}
	return buyer
}

func TestManualRefundHistoryIsKept(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	refund := f.refundFor(t, buyer, 500)
	f.verified(t, buyer, refund.ID)
	if _, err := f.e.pool.Exec(ctx, `DELETE FROM refund_destinations WHERE refund_id = $1`, refund.ID); err == nil {
		t.Fatal("destinations are never deleted")
	}
	if _, err := f.e.pool.Exec(ctx, `UPDATE refund_destinations SET ciphertext = '\x00' WHERE refund_id = $1`, refund.ID); err == nil {
		t.Fatal("a destination's ciphertext is immutable")
	}
	down, err := os.ReadFile("../../migrations/000013_manual_refunds.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("migrating down must refuse while manual refund data exists")
	}
}
