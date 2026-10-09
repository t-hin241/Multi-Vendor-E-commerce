package repository_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
)

// vendorNotices records the HTTP path (EVENT_PUBLISHING=http).
type vendorNotices struct {
	mu   sync.Mutex
	sent map[string]events.VendorOrderAction
}

func (v *vendorNotices) NotifyVendorAction(_ context.Context, eventID string, a events.VendorOrderAction) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sent[eventID] = a
	return nil
}

func vendorNoticeTargets(t *testing.T, pool *pgxpool.Pool, order string) map[string]domain.VendorNoticePayload {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT target, payload FROM order_effects WHERE order_id = $1 AND kind = 'notify_vendor'`, order)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]domain.VendorNoticePayload{}
	for rows.Next() {
		var target string
		var raw []byte
		if err := rows.Scan(&target, &raw); err != nil {
			t.Fatal(err)
		}
		var p domain.VendorNoticePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		out[target] = p
	}
	return out
}

func twoShopOrder(t *testing.T, pool *pgxpool.Pool) (order string, vendorOrders, vendors [2]string) {
	t.Helper()
	order = uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,$2,200,200,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, order, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		vendorOrders[i], vendors[i] = uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(t.Context(), `INSERT INTO vendor_orders(id,order_id,vendor_id,subtotal_amount,currency) VALUES($1,$2,$3,100,'VND')`,
			vendorOrders[i], order, vendors[i]); err != nil {
			t.Fatal(err)
		}
	}
	return order, vendorOrders, vendors
}

// AF-08: a shop hears about a package only once the capture is verified and
// the stock committed, once per package, and the effect reaches
// Notification as order.vendor_action_required (or over HTTP in rollback).
func TestVendorIsToldOfPaidPackagesOnce(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()

	// Stock hold expired: the capture is refused and no shop is told.
	expired := &inventoryReceiptStub{expired: true}
	uc := realUseCase(pool, expired)
	uc.VendorActionNotices = true
	lost, _, _ := twoShopOrder(t, pool)
	if _, err := uc.MarkPaid(ctx, lost, capture(200)); err == nil {
		t.Fatal("a capture without stock must be refused")
	}
	if n := len(vendorNoticeTargets(t, pool, lost)); n != 0 {
		t.Fatalf("shop told about an order without stock: %d", n)
	}

	// Flag off: nothing for shops.
	off := realUseCase(pool, &inventoryReceiptStub{})
	quiet, _, _ := twoShopOrder(t, pool)
	if _, err := off.MarkPaid(ctx, quiet, capture(200)); err != nil {
		t.Fatal(err)
	}
	if n := len(vendorNoticeTargets(t, pool, quiet)); n != 0 {
		t.Fatalf("notices written with the flag off: %d", n)
	}

	bus := &recordedEvents{}
	uc = realUseCase(pool, &inventoryReceiptStub{})
	uc.VendorActionNotices, uc.Events = true, bus
	order, vos, shops := twoShopOrder(t, pool)
	pay := capture(200)
	for range 3 {
		if _, err := uc.MarkPaid(ctx, order, pay); err != nil {
			t.Fatal(err)
		}
	}
	targets := vendorNoticeTargets(t, pool, order)
	if len(targets) != 2 {
		t.Fatalf("expected one notice per package, got %v", targets)
	}
	for i := range 2 {
		p, ok := targets["new_order:"+vos[i]]
		if !ok || p.VendorID != shops[i] || p.VendorOrderID != vos[i] || p.ActionKind != events.VendorActionNewOrder {
			t.Fatalf("package %d: %+v", i, p)
		}
	}
	if _, err := uc.ProcessEffects(ctx, order, 20); err != nil {
		t.Fatal(err)
	}
	published := 0
	for _, env := range bus.sent {
		if env.Type != events.OrderVendorActionRequired {
			continue
		}
		var a events.VendorOrderAction
		if err := env.Decode(&a); err != nil || a.OrderID != order || a.ActionKind != events.VendorActionNewOrder {
			t.Fatalf("event %+v %v", a, err)
		}
		published++
	}
	if published != 2 || countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id=$1 AND kind='notify_vendor' AND status='done'`, order) != 2 {
		t.Fatalf("expected two published notices, got %d", published)
	}

	// Rollback mode: the same effect goes over HTTP under its own id.
	http := &vendorNotices{sent: map[string]events.VendorOrderAction{}}
	uc = realUseCase(pool, &inventoryReceiptStub{})
	uc.VendorActionNotices, uc.VendorNotices = true, http
	other, _, _ := twoShopOrder(t, pool)
	if _, err := uc.MarkPaid(ctx, other, capture(200)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id=$1 AND kind='notify_vendor' AND status='done'`, other) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("HTTP notices not delivered")
		}
		if _, err := uc.ProcessEffects(ctx, other, 20); err != nil {
			t.Fatal(err)
		}
	}
	if len(http.sent) != 2 {
		t.Fatalf("expected two HTTP notices, got %d", len(http.sent))
	}
}

// Return requests, incoming return parcels and buyer cancellation requests
// are the shop's work; a cancellation the shop asked for itself is not.
func TestVendorIsToldOfReturnsAndCancellations(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()

	f := newReturnFixture(t, pool)
	f.uc.VendorActionNotices = true
	f.dests.set(destination(1, "Hà Nội"))
	rr := f.approvedReturn(t)
	if _, _, err := f.dispatch(ctx, rr, "dispatch-key-1", "VN123456789"); err != nil {
		t.Fatal(err)
	}
	targets := vendorNoticeTargets(t, pool, f.order)
	for _, kind := range []string{events.VendorActionReturnRequested, events.VendorActionReturnDispatched} {
		p, ok := targets[kind+":"+rr.ID]
		if !ok || p.VendorID != f.vendor || p.VendorOrderID != f.vendorOrder {
			t.Fatalf("%s missing or wrong: %+v", kind, targets)
		}
	}

	c := newCancelFixture(t, pool)
	c.uc.VendorActionNotices = true
	byBuyer, err := c.request(ctx, "buyer", c.buyer)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := vendorNoticeTargets(t, pool, c.order)[events.VendorActionCancellationRequested+":"+byBuyer.ID]; !ok || p.VendorID != c.vendor {
		t.Fatal("shop not told about the buyer's cancellation request")
	}

	own := newCancelFixture(t, pool)
	own.uc.VendorActionNotices = true
	if _, err := own.request(ctx, "vendor", own.vendorUser); err != nil {
		t.Fatal(err)
	}
	if n := len(vendorNoticeTargets(t, pool, own.order)); n != 0 {
		t.Fatalf("shop told about its own request: %d", n)
	}
}
