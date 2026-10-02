package repository_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/transport"
)

func seedPendingOrder(t *testing.T, pool *pgxpool.Pool) (orderID, buyerID string) {
	t.Helper()
	orderID, buyerID = uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,$2,100,100,'VND','Test Recipient','0000000000','Test','Test','Test','Test street')`, orderID, buyerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO vendor_orders(id,order_id,vendor_id,subtotal_amount,currency) VALUES($1,$2,$3,100,'VND')`,
		uuid.NewString(), orderID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return orderID, buyerID
}

func outcome(t *testing.T, p events.PaymentResult) eventbus.Envelope {
	t.Helper()
	env, err := events.PaymentOutcomeEvent(p)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// PLT-03: a payment outcome event pays the order once, however often it
// is delivered; one Order refuses leaves no partial write and tells Payment
// through a durable effect; one for an unknown order is refused (parked).
func TestPaymentOutcomeEvents(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	uc := realUseCase(pool, &inventoryReceiptStub{})
	inbox := eventbus.Inbox{Pool: pool}
	handle := transport.PaymentOutcomeHandler(uc)

	order, _ := seedPendingOrder(t, pool)
	paymentID := uuid.NewString()
	env := outcome(t, events.PaymentResult{PaymentID: paymentID, OrderID: order, Outcome: "captured", Amount: 100, Currency: "vnd"})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ { // redeliveries, also concurrent
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := inbox.Process(ctx, "order-payment-outcomes", env, handle); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, order).Scan(&status)
	if status != string(domain.StatusPaid) || countRows(t, pool, `SELECT count(*) FROM order_payments WHERE order_id = $1`, order) != 1 {
		t.Fatalf("expected paid once, got %s", status)
	}

	// Wrong amount: refused, nothing kept, Payment told.
	other, _ := seedPendingOrder(t, pool)
	wrong := uuid.NewString()
	if err := inbox.Process(ctx, "order-payment-outcomes", outcome(t, events.PaymentResult{PaymentID: wrong, OrderID: other, Outcome: "captured", Amount: 999, Currency: "VND"}), handle); err != nil {
		t.Fatalf("a refusal is handled (reported), not retried: %v", err)
	}
	_ = pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, other).Scan(&status)
	if status != string(domain.StatusPendingPayment) || countRows(t, pool, `SELECT count(*) FROM order_payments WHERE order_id = $1`, other) != 0 {
		t.Fatal("a refused capture leaves the order untouched")
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM order_effects WHERE order_id = $1 AND kind = 'report_rejected_outcome' AND target = $2`,
		other, "payment-"+wrong).Scan(&payload); err != nil {
		t.Fatalf("expected a rejected-outcome effect: %v", err)
	}
	var p domain.RejectedOutcomePayload
	if err := json.Unmarshal(payload, &p); err != nil || p.Kind != "payment" || p.PaymentID != wrong || p.Reason == "" {
		t.Fatalf("payload %s", payload)
	}

	// Unknown order: refused for good, parked by the consumer.
	err := inbox.Process(ctx, "order-payment-outcomes", outcome(t, events.PaymentResult{PaymentID: uuid.NewString(), OrderID: uuid.NewString(), Outcome: "captured", Amount: 100, Currency: "VND"}), handle)
	if err == nil || !eventbus.IsPermanent(err) {
		t.Fatalf("an unknown order must park, got %v", err)
	}
}

type recordedEvents struct {
	mu   sync.Mutex
	sent []eventbus.Envelope
}

func (r *recordedEvents) Publish(_ context.Context, env eventbus.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, env)
	return nil
}

// Effects that are events are published under the effect id (a retry is
// the same event); the rejected-outcome report reaches Payment that way.
func TestEffectsArePublishedAsEvents(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	bus := &recordedEvents{}
	uc := realUseCase(pool, &inventoryReceiptStub{})
	uc.Events = bus
	order, buyer := seedPendingOrder(t, pool)
	payload, _ := json.Marshal(domain.RejectedOutcomePayload{Kind: "payment", PaymentID: "pay-1", Reason: "amount differs"})
	effects := repository.NewEffectRepository(pool)
	if err := effects.Enqueue(ctx, domain.NewNotifyEffect(order, buyer, "order_paid"),
		domain.Effect{OrderID: order, Kind: domain.EffectReportRejectedOutcome, Target: "payment-pay-1", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.ProcessEffects(ctx, order, 10); err != nil {
		t.Fatal(err)
	}
	if countRows(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND status = 'done'`, order) != 2 || len(bus.sent) != 2 {
		t.Fatalf("expected both effects published and done, got %d events", len(bus.sent))
	}
	byType := map[string]eventbus.Envelope{}
	for _, e := range bus.sent {
		byType[e.Type] = e
	}
	var n events.NotificationRequest
	if err := byType[events.OrderNotificationRequested].Decode(&n); err != nil || n.UserID != buyer || n.Type != "order_paid" || n.ReferenceID != order {
		t.Fatalf("notification event %+v %v", n, err)
	}
	var r events.OutcomeRejection
	if err := byType[events.PaymentOutcomeRejected].Decode(&r); err != nil || r.PaymentID != "pay-1" || r.OrderID != order {
		t.Fatalf("rejection event %+v %v", r, err)
	}
}

// Applying an event never runs effects inline inside the inbox
// transaction (they are not committed yet); the worker runs them.
func TestNoInlineEffectsInsideEventTransaction(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	bus := &recordedEvents{}
	uc := realUseCase(pool, &inventoryReceiptStub{})
	uc.Events = bus
	order, _ := seedPendingOrder(t, pool)
	err := eventbus.Inbox{Pool: pool}.Process(ctx, "order-payment-outcomes",
		outcome(t, events.PaymentResult{PaymentID: uuid.NewString(), OrderID: order, Outcome: "captured", Amount: 100, Currency: "VND"}),
		func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
			return transport.PaymentOutcomeHandler(uc)(ctx, tx, env)
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(bus.sent) != 0 {
		t.Fatal("no event may leave before the transaction that caused it commits")
	}
	if countRows(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND status = 'pending'`, order) == 0 {
		t.Fatal("the paid order's effects wait for the worker")
	}
	if _, err := uc.ProcessEffects(ctx, order, 20); err != nil || len(bus.sent) == 0 {
		t.Fatalf("the worker publishes them: %v", err)
	}
}
