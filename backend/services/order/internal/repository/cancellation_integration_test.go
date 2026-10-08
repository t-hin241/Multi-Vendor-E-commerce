package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// stopShipment stands in for Shipment's stop: the answer is set per test.
type stopShipment struct {
	mu     sync.Mutex
	result string
	calls  int
}

func (s *stopShipment) StopFulfillment(context.Context, string, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.result, nil
}

// recoveryStock stands in for Inventory: a recovery id restocks once.
type recoveryStock struct {
	mu   sync.Mutex
	done map[string]int64
}

func (r *recoveryStock) RestockRecovery(_ context.Context, id, _ string, _ *string, qty int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.done[id]; !ok {
		r.done[id] = qty
	}
	return nil
}

// shopOwner lets the vendor user act for its shop only.
type shopOwner struct{ user, vendor string }

func (s shopOwner) Approved(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (s shopOwner) GetApprovedVendorID(_ context.Context, user, vendor, _ string) (string, error) {
	if user != s.user || (vendor != "" && vendor != s.vendor) {
		return "", apperror.Forbidden("not your shop")
	}
	return s.vendor, nil
}

type cancelFixture struct {
	uc                  *usecase.OrderUseCase
	payment             *holdPayment
	stops               *stopShipment
	stock               *recoveryStock
	buyer, admin, order string
	vendorOrder, vendor string
	vendorUser, item    string
}

func newCancelFixture(t *testing.T, pool *pgxpool.Pool) *cancelFixture {
	t.Helper()
	uc, payment := holdUseCase(pool, "ok")
	f := &cancelFixture{uc: uc, payment: payment, stops: &stopShipment{result: domain.StopStopped}, stock: &recoveryStock{done: map[string]int64{}},
		buyer: uuid.NewString(), admin: uuid.NewString(), vendorUser: uuid.NewString(), item: uuid.NewString()}
	f.order, f.vendorOrder, f.vendor = paidSupportOrder(t, pool, f.buyer)
	if _, err := pool.Exec(t.Context(), `INSERT INTO order_items(id,order_id,vendor_order_id,product_id,product_name,price_amount,quantity,subtotal_amount)
		VALUES($1,$2,$3,gen_random_uuid(),'Test product',50,2,100)`, f.item, f.order, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	uc.Cancellations, uc.Stops, uc.Recoveries, uc.PaidCancellation = repository.CancellationRepository{Pool: pool}, f.stops, f.stock, true
	uc.Vendors = shopOwner{user: f.vendorUser, vendor: f.vendor}
	return f
}

func (f *cancelFixture) request(ctx context.Context, role, actor string) (*domain.CancellationRequest, error) {
	reason := "changed_mind"
	if role == "vendor" {
		reason = "out_of_stock"
	}
	c, _, err := f.uc.RequestCancellation(ctx, usecase.SupportActor{ID: actor, Role: role}, f.vendorOrder,
		usecase.CancellationInput{ReasonCode: reason, Reason: "Không cần nữa"})
	return c, err
}

func countRowsIn(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// AF-03: the request fences the handover, holds the payout, and once an
// admin approves and Shipment confirms nothing left, the stock goes back
// once and Payment is asked for the refund; the buyer hears "refunded"
// only after Payment confirms.
func TestPaidCancellationStopsRestocksAndRefundsOnce(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newCancelFixture(t, pool)
	f.payment.setMode("down")

	var wg sync.WaitGroup
	var opened atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.request(context.Background(), "buyer", f.buyer); err == nil {
				opened.Add(1)
			} else if code(err) != domain.CodeCancellationExists {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if opened.Load() != 1 {
		t.Fatalf("two tabs open one request, got %d", opened.Load())
	}
	open, err := f.uc.ListOrderCancellations(ctx, f.buyer, f.order)
	if err != nil || len(open) != 1 || open[0].Status != domain.CancelPreparing || open[0].HoldStatus != nil {
		t.Fatalf("the buyer sees a preparing request without hold details: %+v %v", open, err)
	}
	id := open[0].ID
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, uuid.NewString()); code(err) != domain.CodeCancellationPending {
		t.Fatalf("an open request fences the handover: %v", err)
	}
	held, err := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{f.vendorOrder})
	if err != nil || held[f.vendorOrder] == "" {
		t.Fatalf("an open request holds the payout: %v %v", held, err)
	}
	if _, err := f.uc.DecideCancellation(ctx, f.admin, id, usecase.CancellationDecision{Decision: "approve", Reason: "ok", ExpectedVersion: open[0].Version}); code(err) != domain.CodeHoldUnavailable {
		t.Fatalf("no decision before the hold is confirmed: %v", err)
	}

	f.payment.setMode("ok")
	runDue(t, pool, f.uc)
	detail, err := f.uc.GetCancellation(ctx, usecase.SupportActor{ID: f.admin, Role: "admin"}, id)
	if err != nil || detail.Request.Status != domain.CancelRequested || *detail.Request.HoldStatus != domain.HoldActive {
		t.Fatalf("hold confirmed, waiting for a decision: %+v %v", detail, err)
	}
	approved, err := f.uc.DecideCancellation(ctx, f.admin, id, usecase.CancellationDecision{Decision: "approve", Reason: "Buyer asked before packing",
		ExpectedVersion: detail.Request.Version})
	if err != nil || approved.Status != domain.CancelStopping || approved.Restock == nil || !*approved.Restock {
		t.Fatalf("approve: %+v %v", approved, err)
	}
	runDue(t, pool, f.uc)
	runDue(t, pool, f.uc)
	detail, err = f.uc.GetCancellation(ctx, usecase.SupportActor{ID: f.admin, Role: "admin"}, id)
	if err != nil || detail.Request.Status != domain.CancelRefundPending || detail.Request.RefundID == nil || *detail.Request.StopResult != domain.StopStopped {
		t.Fatalf("stopped, refund requested: %+v %v", detail.Request, err)
	}
	refund, err := repository.NewRefundRepository(pool).FindByID(ctx, *detail.Request.RefundID)
	if err != nil || refund.ReasonCode != domain.RefundReasonCancellation || refund.Amount != 100 || *refund.VendorOrderID != f.vendorOrder {
		t.Fatalf("refund of what the buyer paid for the package: %+v %v", refund, err)
	}
	if len(f.stock.done) != 1 || f.stock.done[domain.RecoveryID(id, f.item)] != 2 {
		t.Fatalf("each item restocked once: %+v", f.stock.done)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND kind = 'notify' AND target LIKE 'order_refunded:%'`, f.order); n != 0 {
		t.Fatal("the buyer is not told the money is back before Payment confirms")
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND kind = 'notify' AND target LIKE 'cancellation_%'`, f.order); n != 2 {
		t.Fatalf("requested and approved notices, got %d", n)
	}

	if err := f.uc.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: uuid.NewString(), Status: domain.RefundSucceeded,
		Amount: 100, Currency: "VND"}); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	detail, _ = f.uc.GetCancellation(ctx, usecase.SupportActor{ID: f.admin, Role: "admin"}, id)
	if detail.Request.Status != domain.CancelResolved || *detail.Request.HoldStatus != domain.HoldReleased {
		t.Fatalf("resolved, hold released: %+v", detail.Request)
	}
	vo, err := repository.NewVendorOrderRepository(pool).FindByID(ctx, f.vendorOrder)
	if err != nil || vo.Status != domain.StatusRefunded {
		t.Fatalf("the package is refunded only after Payment confirmed: %+v %v", vo, err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_admin_audit WHERE entity_type = 'cancellation_request' AND entity_id = $1`, id); n != 1 {
		t.Fatalf("the decision is audited, got %d", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM cancellation_request_history WHERE request_id = $1`, id); err == nil {
		t.Fatal("history is append-only")
	}
}

// A handover claimed first refuses the cancellation (and the reverse),
// so a cancel and a handover never both win.
func TestHandoverAndCancellationRaceHasOneWinner(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	for i := 0; i < 5; i++ {
		f := newCancelFixture(t, pool)
		var wg sync.WaitGroup
		var claimErr, requestErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			claimErr = f.uc.ClaimHandover(context.Background(), f.vendorOrder, uuid.NewString())
		}()
		go func() {
			defer wg.Done()
			_, requestErr = f.request(context.Background(), "buyer", f.buyer)
		}()
		wg.Wait()
		switch {
		case claimErr == nil && code(requestErr) == domain.CodeAlreadyShipped:
		case requestErr == nil && code(claimErr) == domain.CodeCancellationPending:
		default:
			t.Fatalf("round %d: exactly one must win: claim %v, request %v", i, claimErr, requestErr)
		}
	}
	f := newCancelFixture(t, pool)
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.request(ctx, "vendor", f.vendorUser); code(err) != domain.CodeAlreadyShipped {
		t.Fatalf("after the handover the vendor reports through delivery problems: %v", err)
	}
}

// Shipment says the package already left: the request waits for an admin
// (never cancelled); a failed refund is retried, never dropped.
func TestCancellationAfterHandoverAndFailedRefundNeedReview(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newCancelFixture(t, pool)
	f.stops.result = domain.StopHandedOver
	c, err := f.request(ctx, "vendor", f.vendorUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.request(ctx, "vendor", uuid.NewString()); err == nil {
		t.Fatal("another shop's user cannot request")
	}
	runDue(t, pool, f.uc)
	c, _ = repository.CancellationRepository{Pool: pool}.FindByID(ctx, c.ID)
	approved, err := f.uc.DecideCancellation(ctx, f.admin, c.ID, usecase.CancellationDecision{Decision: "approve", Reason: "Out of stock confirmed",
		ExpectedVersion: c.Version})
	if err != nil || *approved.Restock {
		t.Fatalf("a vendor's shortage is not restocked by default: %+v %v", approved, err)
	}
	runDue(t, pool, f.uc)
	c, _ = repository.CancellationRepository{Pool: pool}.FindByID(ctx, c.ID)
	if c.Status != domain.CancelNeedsReview || c.RefundID != nil || len(f.stock.done) != 0 {
		t.Fatalf("handed over: review, no refund, no restock: %+v", c)
	}
	rejected, err := f.uc.DecideCancellation(ctx, f.admin, c.ID, usecase.CancellationDecision{Decision: "reject", Reason: "Already with the carrier",
		ExpectedVersion: c.Version})
	if err != nil || rejected.Status != domain.CancelRejected {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, uuid.NewString()); err != nil {
		t.Fatalf("a rejected request lifts the fence: %v", err)
	}

	g := newCancelFixture(t, pool)
	c, err = g.request(ctx, "buyer", g.buyer)
	if err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, g.uc)
	c, _ = repository.CancellationRepository{Pool: pool}.FindByID(ctx, c.ID)
	if _, err := g.uc.DecideCancellation(ctx, g.admin, c.ID, usecase.CancellationDecision{Decision: "approve", Reason: "ok", ExpectedVersion: c.Version}); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, g.uc)
	c, _ = repository.CancellationRepository{Pool: pool}.FindByID(ctx, c.ID)
	firstRefund := *c.RefundID
	if err := g.uc.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: firstRefund, Status: domain.RefundFailed, FailureReason: "Bank refused"}); err != nil {
		t.Fatal(err)
	}
	c, _ = repository.CancellationRepository{Pool: pool}.FindByID(ctx, c.ID)
	if c.Status != domain.CancelNeedsReview {
		t.Fatalf("a failed refund needs review: %+v", c)
	}
	if _, err := g.uc.DecideCancellation(ctx, g.admin, c.ID, usecase.CancellationDecision{Decision: "reject", Reason: "x", ExpectedVersion: c.Version}); err == nil {
		t.Fatal("after stock went back the request cannot be rejected; the refund is owed")
	}
	retried, err := g.uc.DecideCancellation(ctx, g.admin, c.ID, usecase.CancellationDecision{Decision: "retry_refund", Reason: "Bank details fixed",
		ExpectedVersion: c.Version})
	if err != nil || retried.Status != domain.CancelRefundPending || *retried.RefundID == firstRefund {
		t.Fatalf("retry opens a new refund: %+v %v", retried, err)
	}
	if len(g.stock.done) != 1 {
		t.Fatal("a refund retry never restocks again")
	}
}
