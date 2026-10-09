package repository_test

import (
	"testing"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

func sourceHold(t *testing.T, holds repository.SourceHoldRepository, sourceType, id string) *domain.SourceHold {
	t.Helper()
	h, err := holds.Get(t.Context(), sourceType, id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func sweep(t *testing.T, uc *usecase.OrderUseCase) int {
	t.Helper()
	n, err := uc.EnsureSourceHolds(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// PW-001: a return and a refund hold the vendor order's payout through
// Payment's ledger from the transaction that opens them; Payment's answer
// is recorded; the hold is released once the source is final; a claim that
// came first needs an operator.
func TestReturnsAndRefundsHoldThePayoutUntilFinal(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	holds := repository.SourceHoldRepository{Pool: pool}
	f.uc.SourceHolds = holds
	payment := f.uc.Holds.(*holdPayment)

	rr, err := f.uc.CreateReturn(ctx, f.buyer, usecase.ReturnInput{OrderID: f.order, ItemID: f.item, Quantity: 1, Reason: "Sai màu"})
	if err != nil {
		t.Fatal(err)
	}
	if h := sourceHold(t, holds, domain.HoldSourceReturn, rr.ID); h == nil || (h.Status != domain.HoldPreparing && h.Status != domain.HoldActive) {
		t.Fatalf("a return opens with its hold: %+v", h)
	}
	runDue(t, pool, f.uc)
	h := sourceHold(t, holds, domain.HoldSourceReturn, rr.ID)
	if h.Status != domain.HoldActive {
		t.Fatalf("hold confirmed by Payment: %+v", h)
	}
	last := payment.acquired[len(payment.acquired)-1]
	if last.SourceType != "return_request" || last.SourceID != rr.ID || last.VendorOrderID != f.vendorOrder || last.VendorID != f.vendor ||
		last.ReasonCode != "return_open" || last.HoldID != h.HoldID {
		t.Fatalf("acquire names the return and vendor order: %+v", last)
	}
	if sweep(t, f.uc) != 0 {
		t.Fatal("an open return's hold must stay")
	}

	if _, err := f.uc.AdminDecideReturn(ctx, f.admin, rr.ID, false, "Không đủ điều kiện trả hàng"); err != nil {
		t.Fatal(err)
	}
	if n := sweep(t, f.uc); n != 1 || sourceHold(t, holds, domain.HoldSourceReturn, rr.ID).Status == domain.HoldActive {
		t.Fatalf("a rejected return's hold goes to release: swept %d, hold %+v", n, sourceHold(t, holds, domain.HoldSourceReturn, rr.ID))
	}
	runDue(t, pool, f.uc)
	if sourceHold(t, holds, domain.HoldSourceReturn, rr.ID).Status != domain.HoldReleased ||
		payment.released[h.HoldID].OperationID != "return_final:"+rr.ID || payment.released[h.HoldID].ResolutionRef != "return:rejected" {
		t.Fatalf("release names the final state: %+v", payment.released[h.HoldID])
	}

	// A refund of the vendor order holds it too, until Payment's outcome.
	refund, err := f.uc.AdminRequestRefund(ctx, f.admin, usecase.RefundInput{OrderID: f.order, VendorOrderID: f.vendorOrder,
		ReasonCode: domain.RefundReasonDispute, Amount: 10, Reason: "Bồi thường giao chậm", IdempotencyKey: "fake-hold-refund-1"})
	if err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	rh := sourceHold(t, holds, domain.HoldSourceRefund, refund.ID)
	if rh == nil || rh.Status != domain.HoldActive || payment.acquired[len(payment.acquired)-1].SourceType != "order_refund" {
		t.Fatalf("refund hold: %+v", rh)
	}
	submitted, err := repository.NewRefundRepository(pool).FindByID(ctx, refund.ID)
	if err != nil || submitted.PaymentRefundID == nil {
		t.Fatalf("refund not submitted: %+v %v", submitted, err)
	}
	if err := f.uc.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: *submitted.PaymentRefundID,
		Status: domain.RefundSucceeded, Amount: 10, Currency: submitted.Currency}); err != nil {
		t.Fatal(err)
	}
	sweep(t, f.uc)
	runDue(t, pool, f.uc)
	if sourceHold(t, holds, domain.HoldSourceRefund, refund.ID).Status != domain.HoldReleased {
		t.Fatal("a refund's hold is released once Payment confirmed it")
	}

	// Payout claimed before the hold: recorded, needs an operator.
	payment.setMode("claimed")
	late, err := f.uc.AdminRequestRefund(ctx, f.admin, usecase.RefundInput{OrderID: f.order, VendorOrderID: f.vendorOrder,
		ReasonCode: domain.RefundReasonDispute, Amount: 5, Reason: "Bồi thường thêm", IdempotencyKey: "fake-hold-refund-2"})
	if err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	if lh := sourceHold(t, holds, domain.HoldSourceRefund, late.ID); lh.Status != domain.HoldNeedsReview || lh.Note == nil {
		t.Fatalf("a claimed payout needs review: %+v", lh)
	}
}

// Returns and refunds opened before the ledger was on are backfilled by the
// sweep; the report's missing count reaches zero (the condition for
// Payment to stop asking Order). With the ledger off nothing is prepared.
func TestSourceHoldsAreBackfilled(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	holds := repository.SourceHoldRepository{Pool: pool}
	f.uc.SupportConfig.HoldLedger = false
	f.uc.SourceHolds = holds

	rr, err := f.uc.CreateReturn(ctx, f.buyer, usecase.ReturnInput{OrderID: f.order, ItemID: f.item, Quantity: 1, Reason: "Không vừa"})
	if err != nil {
		t.Fatal(err)
	}
	refund, err := f.uc.AdminRequestRefund(ctx, f.admin, usecase.RefundInput{OrderID: f.order, VendorOrderID: f.vendorOrder,
		ReasonCode: domain.RefundReasonDispute, Amount: 10, Reason: "Bồi thường", IdempotencyKey: "fake-backfill-refund-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if sourceHold(t, holds, domain.HoldSourceReturn, rr.ID) != nil || sweep(t, f.uc) != 0 {
		t.Fatal("no hold while the ledger is off")
	}
	counts, err := holds.Counts(ctx)
	if err != nil || counts.Missing != 2 {
		t.Fatalf("missing before the backfill: %+v %v", counts, err)
	}

	f.uc.SupportConfig.HoldLedger = true
	if n := sweep(t, f.uc); n != 2 {
		t.Fatalf("backfill prepared %d holds", n)
	}
	runDue(t, pool, f.uc)
	for _, src := range [][2]string{{domain.HoldSourceReturn, rr.ID}, {domain.HoldSourceRefund, refund.ID}} {
		if h := sourceHold(t, holds, src[0], src[1]); h == nil || h.Status != domain.HoldActive {
			t.Fatalf("%s backfilled: %+v", src[0], h)
		}
	}
	if counts, _ = holds.Counts(ctx); counts.Missing != 0 || counts.Preparing != 0 {
		t.Fatalf("after the backfill: %+v", counts)
	}
	if n := sweep(t, f.uc); n != 0 {
		t.Fatalf("a second sweep must change nothing, got %d", n)
	}
}
