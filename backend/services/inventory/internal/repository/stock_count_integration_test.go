package repository_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
)

func countFor(item *domain.InventoryItem, id string, counted int64) *domain.StockCount {
	return &domain.StockCount{ID: id, InventoryItemID: item.ID, CountedOnHand: counted, ActorUserID: uuid.NewString(), Reason: "cycle count"}
}

func TestStockCountWritesDownAvailableWithLedgerAndIdempotency(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	items := repository.NewInventoryItemRepository(pool)
	reservations := repository.NewReservationRepository(pool)
	item := stock(t, pool, 10, false)
	orderID := uuid.NewString()
	if _, err := reservations.ReserveAtomic(ctx, orderID, []domain.ReservationLine{line(item, 3)}); err != nil {
		t.Fatal(err)
	}

	countID := uuid.NewString()
	count, replayed, err := items.RecordStockCount(ctx, countFor(item, countID, 8))
	if err != nil || replayed || count.NewAvailable != 5 || count.PreviousAvailable != 7 || count.ReservedAtCount != 3 {
		t.Fatalf("unexpected count %+v %v %v", count, replayed, err)
	}
	stored, err := items.FindByID(ctx, item.ID)
	if err != nil || stored.AvailableQuantity != 5 || stored.ReservedQuantity != 3 {
		t.Fatalf("reserved must stay untouched, got %+v %v", stored, err)
	}
	assertBalance(t, pool)

	again, replayed, err := items.RecordStockCount(ctx, countFor(item, countID, 8))
	if err != nil || !replayed || again.NewAvailable != 5 {
		t.Fatalf("expected a replay, got %+v %v %v", again, replayed, err)
	}
	if _, _, err := items.RecordStockCount(ctx, countFor(item, countID, 7)); !isConflict(err) {
		t.Fatalf("same id with other values must conflict, got %v", err)
	}
	if _, _, err := items.RecordStockCount(ctx, countFor(item, uuid.NewString(), 2)); !isConflict(err) {
		t.Fatalf("a count below reserved units must conflict, got %v", err)
	}
	if _, _, err := items.RecordStockCount(ctx, countFor(item, uuid.NewString(), 30)); !isConflict(err) {
		t.Fatalf("a count that adds stock must conflict, got %v", err)
	}
	var movements int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE reason='stock_count' AND inventory_item_id=$1`, item.ID).Scan(&movements); err != nil || movements != 1 {
		t.Fatalf("expected exactly one stock_count movement, got %d %v", movements, err)
	}

	// The held units were untouched by the count, so the sale still commits.
	if err := reservations.CommitByOrderID(ctx, orderID); err != nil {
		t.Fatal(err)
	}
	if final, err := items.FindByID(ctx, item.ID); err != nil || final.AvailableQuantity != 5 || final.ReservedQuantity != 0 {
		t.Fatalf("unexpected balance after commit %+v %v", final, err)
	}
	assertBalance(t, pool)
}

func TestStockCountRacingWithCheckoutsNeverOversells(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	items := repository.NewInventoryItemRepository(pool)
	reservations := repository.NewReservationRepository(pool)
	item := stock(t, pool, 20, true)

	var wg sync.WaitGroup
	var mu sync.Mutex
	reserved := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reservations.ReserveAtomic(ctx, uuid.NewString(), []domain.ReservationLine{line(item, 1)}); err == nil {
				mu.Lock()
				reserved++
				mu.Unlock()
			}
		}()
	}
	// Counts land between reservations: each sees the locked, current
	// balance, so it can only lower available and never touch reserved.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			current, err := items.FindByID(ctx, item.ID)
			if err != nil {
				t.Error(err)
				return
			}
			_, _, _ = items.RecordStockCount(ctx, countFor(item, uuid.NewString(), current.AvailableQuantity+current.ReservedQuantity-1))
		}()
	}
	wg.Wait()

	final, err := items.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.AvailableQuantity < 0 || final.ReservedQuantity != int64(reserved) || reserved > 20 {
		t.Fatalf("unexpected balance %+v with %d reservations", final, reserved)
	}
	assertBalance(t, pool)
}

func isConflict(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Code == apperror.CodeConflict
}
