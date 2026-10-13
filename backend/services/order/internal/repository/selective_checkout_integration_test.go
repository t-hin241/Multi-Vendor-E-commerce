package repository_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

func TestDifferentCheckoutKeysSerializePerBuyer(t *testing.T) {
	pool := orderDB(t)
	repo := repository.NewCheckoutOperationRepository(pool)
	buyer := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan *domain.CheckoutOperation, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			op, started, err := repo.Begin(t.Context(), buyer, fmt.Sprintf("concurrent-%d", i), fmt.Sprintf("selection-%d", i), time.Hour)
			if err != nil {
				if e, ok := err.(*apperror.Error); !ok || e.Code != "line_claimed" {
					t.Errorf("unexpected: %v", err)
				}
				return
			}
			if started {
				results <- op
			}
		}(i)
	}
	wg.Wait()
	close(results)
	var winner *domain.CheckoutOperation
	count := 0
	for op := range results {
		count++
		winner = op
	}
	if count != 1 {
		t.Fatalf("%d concurrent claims", count)
	}
	if _, _, err := repo.Begin(t.Context(), uuid.NewString(), "independent-buyer", "hash", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := repo.Fail(t.Context(), winner.ID, apperror.Validation("fake pre-order refusal")); err != nil {
		t.Fatal(err)
	}
	if _, started, err := repo.Begin(t.Context(), buyer, "after-failure", "new-selection", time.Hour); err != nil || !started {
		t.Fatalf("claim not released: %v", err)
	}
}

func TestConsumeBacklogBlocksNewSelectionButNotReplay(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	ops := repository.NewCheckoutOperationRepository(pool)
	op, _, err := ops.Begin(ctx, buyer, "first-selection", "hash-first", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	plan := seedPlan(t, pool, buyer)
	plan.CheckoutOperationID = op.ID
	plan.CartConsumption = &domain.CartConsumption{BuyerID: buyer, OperationID: uuid.NewString(), Lines: []domain.CartConsumeLine{{LineID: uuid.NewString(), Quantity: 3}}}
	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := ops.Complete(ctx, op.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"held", "pending", "parked"} {
		if _, err := pool.Exec(ctx, `UPDATE cart_consumptions SET status=$2 WHERE order_id=$1`, order.ID, status); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ops.Begin(ctx, buyer, "second-selection", "different-lines", time.Hour); err == nil {
			t.Fatalf("%s task lost its claim", status)
		}
		replay, started, err := ops.Begin(ctx, buyer, "first-selection", "hash-first", time.Hour)
		if err != nil || started || replay.OrderID == nil || *replay.OrderID != order.ID {
			t.Fatalf("replay blocked: %+v %v", replay, err)
		}
	}
	if err := repository.NewCartConsumptionRepository(pool).MarkConsumed(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, started, err := ops.Begin(ctx, buyer, "second-selection", "different-lines", time.Hour); err != nil || !started {
		t.Fatalf("consumed claim retained: %v", err)
	}
}
