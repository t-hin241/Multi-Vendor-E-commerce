package repository_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

func holdUseCase(e *env) *usecase.SettlementHoldUseCase {
	return &usecase.SettlementHoldUseCase{Store: repository.SettlementHoldRepository{Pool: e.pool}, Vendors: repository.NewPayoutRepository(e.pool),
		Tx: repository.Transactions{Pool: e.pool}, Log: zerolog.Nop()}
}

func holdOn(vendorID, vendorOrderID string) domain.HoldRequest {
	return domain.HoldRequest{HoldID: uuid.NewString(), VendorID: vendorID, VendorOrderID: vendorOrderID, SourceType: "support_case",
		SourceID: uuid.NewString(), SourceVersion: 1, ReasonCode: "support_case_open"}
}

func release(op string) domain.HoldRelease {
	return domain.HoldRelease{OperationID: op, SourceVersion: 2, ResolutionRef: "no_action", Reason: "Case closed"}
}

func TestSettlementHoldsKeepCreditsOutOfPayouts(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	holds := holdUseCase(e)
	admin, vendor := uuid.NewString(), uuid.NewString()
	old := time.Now().Add(-30 * 24 * time.Hour)
	a, b := settlementOrder(vendor, old), settlementOrder(vendor, old)
	for _, o := range []domain.SettlementOrder{a, b} {
		if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	first := holdOn(vendor, a.VendorOrderID)
	h, created, err := holds.Acquire(ctx, first)
	if err != nil || !created || h.Status != domain.HoldActive || h.PayoutClaimed {
		t.Fatalf("acquire: %+v %v %v", h, created, err)
	}
	if _, created, err := holds.Acquire(ctx, first); err != nil || created {
		t.Fatalf("a replay returns the same hold: %v %v", created, err)
	}
	changed := first
	changed.ReasonCode = "other_reason"
	if _, _, err := holds.Acquire(ctx, changed); appCode(err) != "hold_conflict" {
		t.Fatalf("the same hold id with another payload must conflict: %v", err)
	}
	sameSource := first
	sameSource.HoldID = uuid.NewString()
	if _, _, err := holds.Acquire(ctx, sameSource); appCode(err) != "hold_conflict" {
		t.Fatalf("one source holds a vendor order once: %v", err)
	}
	second := holdOn(vendor, a.VendorOrderID)
	if _, _, err := holds.Acquire(ctx, second); err != nil {
		t.Fatal(err)
	}

	batch, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-hold-batch-1", "VND", nil)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Amount != 110000 {
		t.Fatalf("only the vendor order without a hold is paid: %+v %v", batch, err)
	}

	receipt, err := holds.Release(ctx, first.HoldID, release("fake-release-op-1"))
	if err != nil || receipt.Status != domain.HoldReleased {
		t.Fatalf("release: %+v %v", receipt, err)
	}
	again, err := holds.Release(ctx, first.HoldID, release("fake-release-op-1"))
	if err != nil || *again.ReleaseOperationID != "fake-release-op-1" || !again.ReleasedAt.Equal(*receipt.ReleasedAt) {
		t.Fatalf("a repeated release returns the same receipt: %+v %v", again, err)
	}
	if _, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-hold-batch-2", "VND", nil); err == nil {
		t.Fatal("releasing one hold must not release another on the same vendor order")
	}
	if _, err := holds.Release(ctx, second.HoldID, release("fake-release-op-2")); err != nil {
		t.Fatal(err)
	}
	batch, _, err = e.settle.CreatePayoutBatch(ctx, admin, "fake-hold-batch-3", "VND", nil)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Amount != 110000 {
		t.Fatalf("released vendor order is paid: %+v %v", batch, err)
	}

	// A hold after the payout claimed the vendor order is recorded but
	// flagged: the source must not report the money as protected.
	late, _, err := holds.Acquire(ctx, holdOn(vendor, b.VendorOrderID))
	if err != nil || !late.PayoutClaimed || late.Status != domain.HoldActive {
		t.Fatalf("a hold after the claim must be flagged: %+v %v", late, err)
	}

	// A release that arrives before its acquire leaves a tombstone.
	early := holdOn(vendor, a.VendorOrderID)
	if _, err := holds.Release(ctx, early.HoldID, release("fake-release-op-3")); err != nil {
		t.Fatal(err)
	}
	tomb, created, err := holds.Acquire(ctx, early)
	if err != nil || created || tomb.Status != domain.HoldReleased {
		t.Fatalf("a late acquire must not reopen a released hold: %+v %v %v", tomb, created, err)
	}
	if _, err := e.pool.Exec(ctx, `DELETE FROM settlement_holds WHERE id = $1`, early.HoldID); err == nil {
		t.Fatal("holds are never deleted")
	}
}

// A hold and a payout claim on the same vendor order are ordered by the
// vendor lock: either the batch skips the vendor order, or the hold sees
// the claim. Never a claim the hold does not know about.
func TestHoldAndPayoutClaimNeverInterleave(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	holds := holdUseCase(e)
	admin := uuid.NewString()
	old := time.Now().Add(-30 * 24 * time.Hour)
	for i := 0; i < 6; i++ {
		vendor := uuid.NewString()
		o := settlementOrder(vendor, old)
		if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var hold *domain.SettlementHold
		var batch *domain.PayoutBatch
		var holdErr, batchErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			hold, _, holdErr = holds.Acquire(context.Background(), holdOn(vendor, o.VendorOrderID))
		}()
		go func() {
			defer wg.Done()
			batch, _, batchErr = e.settle.CreatePayoutBatch(context.Background(), admin, "fake-race-batch-"+uuid.NewString()[:8], "VND", []string{vendor})
		}()
		wg.Wait()
		if holdErr != nil {
			t.Fatal(holdErr)
		}
		claimed := batchErr == nil && len(batch.Items) == 1
		if claimed != hold.PayoutClaimed {
			t.Fatalf("round %d: batch claimed=%v but hold saw claimed=%v (batch err %v)", i, claimed, hold.PayoutClaimed, batchErr)
		}
	}
}
