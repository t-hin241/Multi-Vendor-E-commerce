package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

func TestVacationCheckoutFenceAndExistingUnpaidOrder(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	orders := repository.NewOrderRepository(pool)
	plan := seedPlan(t, pool, uuid.NewString())
	order, err := orders.CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	var vendor string
	for id := range plan.VendorVersions {
		vendor = id
	}
	paused, open := false, true
	store := vendorsales.Store{Pool: pool}
	if err := store.Apply(ctx, vendorsales.Status{VendorID: vendor, Status: "approved", Version: 2, AcceptingOrders: &paused}); err != nil {
		t.Fatal(err)
	}
	// The old plan was computed before cutover and cannot create another order.
	if _, err := orders.CreateFromPlan(ctx, plan); err == nil {
		t.Fatal("old checkout crossed vacation fence")
	} else {
		var app *apperror.Error
		if !errors.As(err, &app) || app.Code != "vendor_not_accepting_orders" {
			t.Fatalf("fence error: %v", err)
		}
	}
	// Neither a delayed old event nor conflicting equal-version reconciliation reopens it.
	for _, version := range []int64{1, 2} {
		if err := store.ApplyAll(ctx, []vendorsales.Status{{VendorID: vendor, Status: "approved", Version: version, AcceptingOrders: &open}}); err != nil {
			t.Fatal(err)
		}
	}
	var accepting bool
	if err := pool.QueryRow(ctx, `SELECT accepting_orders FROM vendor_sale_status WHERE vendor_id=$1`, vendor).Scan(&accepting); err != nil || accepting {
		t.Fatalf("stale event reopened shop: %v", err)
	}
	// A reservation accepted before cutover can still become paid and queue fulfillment.
	if _, err := pool.Exec(ctx, `UPDATE orders SET checkout_state='ready' WHERE id=$1`, order.ID); err != nil {
		t.Fatal(err)
	}
	paid, err := realUseCase(pool, &inventoryReceiptStub{}).MarkPaid(ctx, order.ID, capture(order.TotalAmount))
	if err != nil || paid.Status != domain.StatusPaid {
		t.Fatalf("existing unpaid order blocked: %+v %v", paid, err)
	}
	var effects int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_effects WHERE order_id=$1 AND kind='create_shipment'`, order.ID).Scan(&effects); err != nil || effects != 1 {
		t.Fatalf("old order fulfillment: %d %v", effects, err)
	}
	if err := store.Apply(ctx, vendorsales.Status{VendorID: vendor, Status: "approved", Version: 3, AcceptingOrders: &open}); err != nil {
		t.Fatal(err)
	}
	if _, err := orders.CreateFromPlan(ctx, plan); err == nil {
		t.Fatal("pre-vacation version accepted after resume")
	}
	plan.VendorVersions[vendor] = 3
	if _, err := orders.CreateFromPlan(ctx, plan); err != nil {
		t.Fatal("fresh checkout after resume:", err)
	}
}

func TestVacationCutoverWaitsForAcceptedCheckoutFence(t *testing.T) {
	pool := orderDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	plan := seedPlan(t, pool, uuid.NewString())
	var vendor string
	for id := range plan.VendorVersions {
		vendor = id
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := vendorsales.LockApproved(ctx, tx, plan.VendorVersions); err != nil {
		t.Fatal(err)
	}
	// A competing transaction proves the row lock is held, without timing sleeps.
	cutover, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cutover.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := cutover.Exec(ctx, `SET LOCAL lock_timeout='50ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := cutover.Exec(ctx, `UPDATE vendor_sale_status SET accepting_orders=false, version=2 WHERE vendor_id=$1`, vendor); err == nil {
		t.Fatal("cutover overtook a checkout holding the fence")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	paused := false
	if err := (vendorsales.Store{Pool: pool}).Apply(ctx, vendorsales.Status{VendorID: vendor, Status: "approved", Version: 2, AcceptingOrders: &paused}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, plan); err == nil {
		t.Fatal("checkout after cutover accepted")
	}
}
