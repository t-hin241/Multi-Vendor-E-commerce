package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

func TestCartConsumptionIsCreatedWithTheOrderAndRetriedDurably(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	vendor, product, buyer, operation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version) VALUES($1,'approved',1)`, vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_sale_status(product_id,is_visible,version) VALUES($1,true,1)`, product); err != nil {
		t.Fatal(err)
	}

	plan, err := domain.BuildCheckoutPlan(buyer, []domain.CheckoutLine{{ProductID: product, VendorID: vendor, ProductName: "P", PriceAmount: 100, Currency: "VND", Quantity: 2}})
	if err != nil {
		t.Fatal(err)
	}
	plan.VendorVersions = map[string]int64{vendor: 1}
	plan.ProductVersions = map[string]int64{product: 1}
	plan.Order.RecipientName, plan.Order.Phone, plan.Order.Province, plan.Order.District, plan.Order.Ward, plan.Order.StreetAddress =
		"Test Recipient", "0000000000", "Test", "Test", "Test", "Test street"
	lineID := uuid.NewString()
	plan.CartConsumption = &domain.CartConsumption{BuyerID: buyer, OperationID: operation, Lines: []domain.CartConsumeLine{{LineID: lineID, Quantity: 2}}}

	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewCartConsumptionRepository(pool)

	open, err := repo.ListOpenByBuyer(ctx, buyer)
	if err != nil || len(open) != 1 || open[0].Status != domain.CartConsumptionHeld || open[0].OrderID != order.ID ||
		open[0].OperationID != operation || open[0].Lines[0].LineID != lineID {
		t.Fatalf("expected one held task written with the order, got %+v %v", open, err)
	}
	if due, err := repo.ClaimDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("a held task must not be consumed before the reservation, got %d %v", len(due), err)
	}

	if err := repo.Activate(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	due, err := repo.ClaimDue(ctx, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("expected the activated task to be due, got %d %v", len(due), err)
	}
	if again, err := repo.ClaimDue(ctx, 10); err != nil || len(again) != 0 {
		t.Fatalf("a claimed task must be leased away from other workers, got %d %v", len(again), err)
	}

	if err := repo.RecordFailure(ctx, order.ID, "cart service unreachable", false); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, next_attempt_at FROM cart_consumptions WHERE order_id=$1`, order.ID).Scan(&attempts, &next); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || time.Until(next) < 3*time.Second || time.Until(next) > 10*time.Second {
		t.Fatalf("expected a ~5s backoff after the first failure, got attempts=%d next in %v", attempts, time.Until(next))
	}

	stats, err := repo.Stats(ctx)
	if err != nil || stats.Pending != 1 || stats.OldestPending == nil {
		t.Fatalf("unexpected stats %+v %v", stats, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE cart_consumptions SET next_attempt_at = now() - interval '1 second' WHERE order_id=$1`, order.ID); err != nil {
		t.Fatal(err)
	}
	if due, err := repo.ClaimDue(ctx, 10); err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("expected the task to come back after its backoff, got %+v %v", due, err)
	}
	if err := repo.MarkConsumed(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	if open, err := repo.ListOpenByBuyer(ctx, buyer); err != nil || len(open) != 0 {
		t.Fatalf("a consumed task must not block the buyer, got %d %v", len(open), err)
	}
}

func TestCartConsumptionParksAndCancels(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	repo := repository.NewCartConsumptionRepository(pool)
	insert := func(status string) string {
		order := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO orders(id,buyer_id,total_amount,currency,recipient_name,phone,province,district,ward,street_address) VALUES($1,$2,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, order, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cart_consumptions(order_id,buyer_id,operation_id,lines,status) VALUES($1,$2,$3,'[{"line_id":"x","quantity":1}]',$4)`, order, uuid.NewString(), uuid.NewString(), status); err != nil {
			t.Fatal(err)
		}
		return order
	}

	refused := insert("pending")
	if err := repo.RecordFailure(ctx, refused, "Checkout operation not found", true); err != nil {
		t.Fatal(err)
	}
	exhausted := insert("pending")
	if _, err := pool.Exec(ctx, `UPDATE cart_consumptions SET attempts=$2 WHERE order_id=$1`, exhausted, domain.MaxCartConsumeAttempts-1); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordFailure(ctx, exhausted, "timeout", false); err != nil {
		t.Fatal(err)
	}
	held := insert("held")
	if err := repo.Cancel(ctx, held); err != nil {
		t.Fatal(err)
	}
	pending := insert("pending")
	if err := repo.Cancel(ctx, pending); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{refused: "parked", exhausted: "parked", held: "cancelled", pending: "pending"}
	for order, status := range want {
		var got string
		if err := pool.QueryRow(ctx, `SELECT status FROM cart_consumptions WHERE order_id=$1`, order).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != status {
			t.Errorf("order %s: expected %s, got %s", order, status, got)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cart_consumptions(order_id,buyer_id,operation_id,lines) VALUES($1,$2,$3,'[]')`, insert("held"), uuid.NewString(), uuid.NewString()); err == nil {
		t.Error("a task without lines must be rejected")
	}
}
