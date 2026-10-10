package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

type refundDeadline struct {
	stage   string
	active  bool
	due     time.Time
	version string
}

func deadlineOf(t *testing.T, f *manualFixture, refundID string) *refundDeadline {
	t.Helper()
	var d refundDeadline
	err := f.e.pool.QueryRow(t.Context(), `SELECT payload->>'stage', active, due_at, payload->>'deadline_version' FROM case_sla_work_items
		WHERE resource_type = 'refund' AND resource_id = $1`, refundID).Scan(&d.stage, &d.active, &d.due, &d.version)
	if err != nil {
		return nil
	}
	return &d
}

func near(a, b time.Time) bool { d := a.Sub(b); return d > -time.Minute && d < time.Minute }

// PW-017: under the manual workflow the admin's clock runs only while the
// work is the marketplace's: 24h to verify a submitted account, 72h to
// transfer once it is ready (a claim does not restart it); waiting for the
// buyer's account and a submitted transfer carry no admin deadline. Refunds
// opened before the workflow are moved off the old receipt deadline.
func TestManualRefundDeadlinesFollowTheWorkflow(t *testing.T) {
	f := newManualFixture(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	store := repository.ManualRefundRepository{Pool: f.e.pool}

	legacy := f.refundFor(t, buyer, 500)
	if d := deadlineOf(t, f, legacy.ID); d == nil || d.stage != "awaiting_refund_receipt" || !d.active {
		t.Fatalf("a refund before the workflow has the receipt deadline: %+v", d)
	}
	if n, err := store.RestageLegacySLA(ctx, 10); err != nil || n < 1 {
		t.Fatalf("restage: %d %v", n, err)
	}
	if d := deadlineOf(t, f, legacy.ID); d == nil || d.active {
		t.Fatalf("waiting for the buyer's account is not the admin's time: %+v", d)
	}

	order, intent := uuid.NewString(), uuid.NewString()
	if _, err := f.e.pool.Exec(ctx, `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id)
		VALUES($1,$2,$3,2000,'VND','captured','test',$4)`, intent, order, buyer, "fake-ref-"+intent); err != nil {
		t.Fatal(err)
	}
	req := refundReq(order, 700)
	refund, _, err := repository.NewRefundRepository(f.e.pool).WithManualSLA(true).Request(ctx, req, check(req))
	if err != nil {
		t.Fatal(err)
	}
	if d := deadlineOf(t, f, refund.ID); d != nil {
		t.Fatalf("no admin deadline before the buyer gives an account: %+v", d)
	}

	if _, err := f.uc.SubmitDestination(ctx, buyer, refund.ID, fakeBeneficiary, 0); err != nil {
		t.Fatal(err)
	}
	d := deadlineOf(t, f, refund.ID)
	if d == nil || d.stage != "refund_destination_verification" || !d.active || !near(d.due, f.clock.Load().Add(24*time.Hour)) {
		t.Fatalf("finance verifies within 24h: %+v", d)
	}

	f.advance(2 * time.Hour)
	proof := f.auth.proof(f.approverA, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(refund.ID, 1, true))
	if _, err := f.uc.DecideDestination(ctx, f.approverA, refund.ID, usecase.DestinationDecision{Version: 1, Verify: true, Reason: "Name matches", Proof: proof}); err != nil {
		t.Fatal(err)
	}
	ready := deadlineOf(t, f, refund.ID)
	if ready == nil || ready.stage != "manual_refund_ready" || !ready.active || !near(ready.due, f.clock.Load().Add(72*time.Hour)) {
		t.Fatalf("72h from ready: %+v", ready)
	}

	attempt, err := f.uc.PrepareAttempt(ctx, f.preparer, refund.ID, 1, "Transfer refund")
	if err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	claimed, err := f.uc.Claim(ctx, f.preparer, attempt.ID, attempt.Version)
	if err != nil {
		t.Fatal(err)
	}
	if d := deadlineOf(t, f, refund.ID); d == nil || d.version != ready.version || !d.due.Equal(ready.due) {
		t.Fatalf("preparing and claiming keep the ready deadline: %+v vs %+v", d, ready)
	}

	if _, err := f.uc.Submit(ctx, f.preparer, attempt.ID, usecase.SubmitInput{ExpectedVersion: claimed.Version,
		Submission: domain.Submission{BankReference: "FAKE-FT-0917", SourceAccount: "PLATFORM-FAKEBANK", ExecutedAt: f.clock.Load().Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if d := deadlineOf(t, f, refund.ID); d == nil || d.active {
		t.Fatalf("a submitted transfer leaves the ready deadline: %+v", d)
	}
}
