package repository_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// PW-001: a hold that arrives after a payout claimed the vendor order is
// recorded (payout_claimed); an operator cancels the untransferred item
// with a reason (audited), its credits are unpaid again and the next batch
// keeps them while the hold is active. No shop notice for a cancellation.
func TestCancelledClaimLetsTheLateHoldProtectTheCredits(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	admin, vendor := uuid.NewString(), uuid.NewString()
	o := settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))
	if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	notices := repository.VendorNotices{Pool: e.pool}
	e.settle.VendorNotices = notices
	batch, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-cancel-batch-1", "VND", nil)
	if err != nil || len(batch.Items) != 1 {
		t.Fatalf("batch %+v %v", batch, err)
	}
	item := batch.Items[0]

	holds := &usecase.SettlementHoldUseCase{Store: repository.SettlementHoldRepository{Pool: e.pool}, Vendors: repository.NewPayoutRepository(e.pool),
		Tx: repository.Transactions{Pool: e.pool}, Log: zerolog.Nop()}
	hold, _, err := holds.Acquire(ctx, domain.HoldRequest{HoldID: uuid.NewString(), VendorID: vendor, VendorOrderID: o.VendorOrderID,
		SourceType: "return_request", SourceID: uuid.NewString(), SourceVersion: 1, ReasonCode: "return_open"})
	if err != nil || !hold.PayoutClaimed {
		t.Fatalf("a hold after the claim is recorded as claimed: %+v %v", hold, err)
	}

	if _, err := e.settle.CancelPayoutItem(ctx, admin, item.ID, ""); err == nil {
		t.Fatal("a cancellation needs a reason")
	}
	cancelled, err := e.settle.CancelPayoutItem(ctx, admin, item.ID, "Return opened after the claim; transfer not made")
	if err != nil || cancelled.Status != domain.PayoutItemCancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	var app *apperror.Error
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, item.ID, domain.PayoutResolution{Outcome: domain.PayoutItemSucceeded, EvidenceReference: "FAKE-1"}); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatalf("a cancelled item cannot be resolved: %v", err)
	}
	if _, err := e.settle.CancelPayoutItem(ctx, admin, item.ID, "again"); err == nil {
		t.Fatal("a cancelled item cannot be cancelled again")
	}
	if e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE action = 'payout_item_cancelled' AND target_id = $1`, item.ID) != 1 {
		t.Fatal("cancellation not audited")
	}
	if e.count(t, `SELECT count(*) FROM payment_vendor_notices`) != 0 {
		t.Fatal("no shop notice for a cancellation")
	}

	// The credits are unpaid again, and the active hold keeps them out.
	if _, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-cancel-batch-2", "VND", nil); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatalf("held credits must not be paid: %v", err)
	}
	if _, err := holds.Release(ctx, hold.ID, domain.HoldRelease{OperationID: "return_final:x", SourceVersion: 2, ResolutionRef: "return:rejected", Reason: "Return rejected"}); err != nil {
		t.Fatal(err)
	}
	again, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-cancel-batch-3", "VND", nil)
	if err != nil || len(again.Items) != 1 || again.Items[0].Amount != item.Amount {
		t.Fatalf("released credits are paid in the next batch: %+v %v", again, err)
	}
}

// SETTLEMENT_ORDER_HOLD_QUERY=off: Order is no longer asked; only the
// ledger holds count.
func TestOrderHoldQueryCanBeTurnedOff(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	admin, vendor := uuid.NewString(), uuid.NewString()
	o := settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))
	if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	e.orders.held[o.VendorOrderID] = "return_open"
	if _, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-query-batch-1", "VND", nil); err == nil {
		t.Fatal("with the query on, Order's hold keeps the credits")
	}
	e.settle.SkipOrderHoldQuery = true
	batch, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-query-batch-2", "VND", nil)
	if err != nil || len(batch.Items) != 1 {
		t.Fatalf("with the query off only the ledger counts: %+v %v", batch, err)
	}
}
