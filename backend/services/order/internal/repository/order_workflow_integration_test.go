package repository_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// seedPlan builds a one-vendor plan whose shop and product are sellable.
func seedPlan(t *testing.T, pool *pgxpool.Pool, buyerID string) *domain.Plan {
	t.Helper()
	ctx := t.Context()
	vendor, product := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version) VALUES($1,'approved',1)`, vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_sale_status(product_id,is_visible,version) VALUES($1,true,1)`, product); err != nil {
		t.Fatal(err)
	}
	plan, err := domain.BuildCheckoutPlan(buyerID, []domain.CheckoutLine{{ProductID: product, VendorID: vendor, ProductName: "P", PriceAmount: 1000, Currency: "VND", Quantity: 3}})
	if err != nil {
		t.Fatal(err)
	}
	plan.VendorVersions, plan.ProductVersions = map[string]int64{vendor: 1}, map[string]int64{product: 1}
	plan.Order.RecipientName, plan.Order.Phone, plan.Order.Province, plan.Order.District, plan.Order.Ward, plan.Order.StreetAddress = "R", "0900000000", "HN", "D", "W", "S"
	quote := domain.ShippingQuote{VendorID: vendor, FeeAmount: 15000, Currency: "VND", CarrierID: uuid.NewString(), ZoneID: uuid.NewString(),
		FeeRuleID: uuid.NewString(), FeeRuleVersion: 3, PackageWeightGrams: 1500, QuotedAt: time.Now().UTC()}
	if err := plan.ApplyShipping(map[string]domain.ShippingQuote{vendor: quote}); err != nil {
		t.Fatal(err)
	}
	rule, err := repository.NewCommissionRuleRepository(pool).FindCurrent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ApplyCommission(rule); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCreateFromPlanStoresSnapshotsAndLinksCheckoutAtomically(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	ops := repository.NewCheckoutOperationRepository(pool)
	op, started, err := ops.Begin(ctx, buyer, "key-snapshot-1", "hash", time.Hour)
	if err != nil || !started {
		t.Fatalf("begin: %v %v", started, err)
	}
	plan := seedPlan(t, pool, buyer)
	plan.CheckoutOperationID = op.ID
	orders := repository.NewOrderRepository(pool)
	order, err := orders.CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if order.CheckoutState != domain.CheckoutPreparing || order.TotalAmount != 18000 || order.ShippingAmount != 15000 {
		t.Fatalf("unexpected order %+v", order)
	}
	vos, err := repository.NewVendorOrderRepository(pool).ListByOrderID(ctx, order.ID)
	if err != nil || len(vos) != 1 {
		t.Fatal(err)
	}
	vo := vos[0]
	if vo.Shipping == nil || vo.Shipping.FeeRuleVersion != 3 || vo.Shipping.PackageWeightGrams != 1500 || vo.ShippingFeeAmount != 15000 {
		t.Fatalf("shipping snapshot lost: %+v", vo.Shipping)
	}
	if vo.Commission == nil || vo.Commission.Source != domain.CommissionSourceCheckout || *vo.Commission.RuleVersion != 1 ||
		vo.Commission.BaseAmount != 3000 || vo.Commission.Amount != 300 || vo.Commission.Rounding != "floor" {
		t.Fatalf("commission snapshot lost: %+v", vo.Commission)
	}
	stored, _ := ops.FindByID(ctx, op.ID)
	if stored.OrderID == nil || *stored.OrderID != order.ID {
		t.Fatal("the checkout operation must be linked in the same transaction")
	}
	// A second order can never be linked to the same operation.
	plan2 := seedPlan(t, pool, buyer)
	plan2.CheckoutOperationID = op.ID
	if _, err := orders.CreateFromPlan(ctx, plan2); err == nil {
		t.Fatal("expected the second link to fail")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE buyer_id=$1`, buyer).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the failed order must roll back entirely, got %d orders", count)
	}
}

func TestCheckoutOperationBeginIsExclusiveAndReplaysOutcomes(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	ops := repository.NewCheckoutOperationRepository(pool)
	buyer := uuid.NewString()

	var wg sync.WaitGroup
	var mu sync.Mutex
	started := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, s, err := ops.Begin(context.Background(), buyer, "key-exclusive", "hash-a", time.Hour)
			if err != nil {
				t.Error(err)
				return
			}
			if s {
				mu.Lock()
				started++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("exactly one request may own the key, got %d", started)
	}
	op, _, _ := ops.Begin(ctx, buyer, "key-exclusive", "hash-a", time.Hour)
	if err := ops.Fail(ctx, op.ID, apperror.Internal(errors.New("outage"))); err != nil {
		t.Fatal(err)
	}
	if _, s, _ := ops.Begin(ctx, buyer, "key-exclusive", "hash-a", time.Hour); !s {
		t.Fatal("an infrastructure failure must be retryable with the same key")
	}
	if err := ops.Fail(ctx, op.ID, apperror.Conflict("Not enough stock")); err != nil {
		t.Fatal(err)
	}
	again, s, _ := ops.Begin(ctx, buyer, "key-exclusive", "hash-a", time.Hour)
	if s || again.StoredError().Message != "Not enough stock" {
		t.Fatalf("a business failure must replay, got started=%v %+v", s, again)
	}
	other, s, _ := ops.Begin(ctx, buyer, "key-exclusive", "hash-b", time.Hour)
	if s || other.RequestHash != "hash-a" {
		t.Fatal("a different request must see the stored one (use case refuses it)")
	}
}

func TestOrderTransitionsAreCompareAndSet(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	plan := seedPlan(t, pool, uuid.NewString())
	orders := repository.NewOrderRepository(pool)
	order, err := orders.CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, to := range []domain.Status{domain.StatusPaid, domain.StatusCancelled} {
		wg.Add(1)
		go func(to domain.Status) {
			defer wg.Done()
			results <- orders.TransitionStatus(context.Background(), order.ID, domain.StatusPendingPayment, to, nil)
		}(to)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, repository.ErrStaleState) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one transition may win, got %d", wins)
	}
	final, _ := orders.FindByID(ctx, order.ID)
	if final.Version != 2 {
		t.Fatalf("expected one version bump, got %d", final.Version)
	}
}

func TestRefundsReturnsAndEffectsPersistence(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	plan := seedPlan(t, pool, buyer)
	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := repository.NewOrderRepository(pool).ListItemsByOrder(ctx, order.ID)
	returns := repository.NewReturnRequestRepository(pool)
	rr := &domain.ReturnRequest{OrderID: order.ID, OrderItemID: items[0].ID, BuyerID: buyer, Reason: "broken", Quantity: 1, RefundAmount: 1000,
		PolicyVersion: "window-7d", ReturnWindowDays: ptrInt(7)}
	if err := returns.Create(ctx, rr); err != nil {
		t.Fatal(err)
	}
	if err := returns.Create(ctx, &domain.ReturnRequest{OrderID: order.ID, OrderItemID: items[0].ID, BuyerID: buyer, Reason: "x", Quantity: 1, RefundAmount: 1, PolicyVersion: "v"}); err == nil {
		t.Fatal("one return per item")
	}
	stale := *rr
	if err := returns.Transition(ctx, rr, domain.ReturnApproved, repository.ReturnUpdate{DecidedBy: ptrStr(uuid.NewString())}); err != nil {
		t.Fatal(err)
	}
	if err := returns.Transition(ctx, &stale, domain.ReturnRejected, repository.ReturnUpdate{}); !errors.Is(err, repository.ErrStaleState) {
		t.Fatalf("a stale version must lose, got %v", err)
	}
	if err := returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorRole: "admin", Action: "approved", ToStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := returns.FindByID(ctx, rr.ID)
	if stored.Status != domain.ReturnApproved || stored.DecidedAt == nil {
		t.Fatalf("unexpected return %+v", stored)
	}

	refunds := repository.NewRefundRepository(pool)
	vendorOrderID := itemsVendor(items)
	first := &domain.Refund{OrderID: order.ID, VendorOrderID: &vendorOrderID, ReturnRequestID: &rr.ID, ReasonCode: "return", Amount: 1000, Currency: "VND", Reason: "r", RequestedBy: uuid.NewString()}
	if err := refunds.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	dup := *first
	if err := refunds.Create(ctx, &dup); err == nil {
		t.Fatal("a second open refund for one return must be refused")
	}
	orderOpen, vendorOpen, err := refunds.OpenTotals(ctx, order.ID, vendorOrderID)
	if err != nil || orderOpen != 1000 || vendorOpen != 1000 {
		t.Fatalf("unexpected open totals %d %d %v", orderOpen, vendorOpen, err)
	}
	if err := refunds.Transition(ctx, first.ID, domain.RefundRequested, domain.RefundFailed, nil, ptrStr("bank")); err != nil {
		t.Fatal(err)
	}
	retry := *first
	if err := refunds.Create(ctx, &retry); err != nil {
		t.Fatalf("a retry after a failed refund must be allowed: %v", err)
	}

	effects := repository.NewEffectRepository(pool)
	e := domain.Effect{OrderID: order.ID, Kind: domain.EffectRequestRefund, Target: retry.ID}
	if err := effects.Enqueue(ctx, e, e); err != nil {
		t.Fatal(err)
	}
	claimed, err := effects.ClaimDue(ctx, "", 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("expected one due effect, got %d %v", len(claimed), err)
	}
	if again, _ := effects.ClaimDue(ctx, "", 10); len(again) != 0 {
		t.Fatal("a claimed effect must be leased")
	}
	if parked, err := effects.RecordFailure(ctx, claimed[0].ID, "payment down", false); err != nil || parked {
		t.Fatalf("expected a retry, got parked=%v %v", parked, err)
	}
	if parked, _ := effects.RecordFailure(ctx, claimed[0].ID, "refused", true); !parked {
		t.Fatal("a permanent failure must park")
	}
	if ok, _ := effects.Replay(ctx, claimed[0].ID); !ok {
		t.Fatal("a parked effect must be replayable")
	}
	stats, _ := effects.Stats(ctx)
	if stats.Pending != 1 || stats.Parked != 0 {
		t.Fatalf("unexpected stats %+v", stats)
	}
}

func TestCommissionRuleVersionsAreSequentialUnderConcurrency(t *testing.T) {
	pool := orderDB(t)
	rules := repository.NewCommissionRuleRepository(pool)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rules.Create(context.Background(), &domain.CommissionRule{RateBps: 100 * i}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	current, err := rules.FindCurrent(t.Context())
	if err != nil || current.Version != 11 {
		t.Fatalf("expected version 11 after the seeded rule and 10 more, got %+v %v", current, err)
	}
}

func TestMigrationsBackfillLegacyOrdersWithoutInventingHistory(t *testing.T) {
	pool := orderDB(t, "000009")
	ctx := t.Context()
	order, vo, item, rr := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	stmts := []string{
		`INSERT INTO orders(id,buyer_id,status,total_amount,currency,recipient_name,phone,province,district,ward,street_address) VALUES('` + order + `',gen_random_uuid(),'completed',25000,'VND','R','0','P','D','W','S')`,
		`INSERT INTO vendor_orders(id,order_id,vendor_id,status,subtotal_amount,shipping_fee_amount,currency,commission_rate_bps,commission_amount,net_amount) VALUES('` + vo + `','` + order + `',gen_random_uuid(),'completed',20000,5000,'VND',1000,2000,18000)`,
		`INSERT INTO order_items(id,order_id,vendor_order_id,product_id,product_name,price_amount,quantity,subtotal_amount) VALUES('` + item + `','` + order + `','` + vo + `',gen_random_uuid(),'P',10000,2,20000)`,
		`INSERT INTO return_requests(id,order_id,order_item_id,buyer_id,reason,status) VALUES('` + rr + `','` + order + `','` + item + `',gen_random_uuid(),'broken','approved_awaiting_provider_refund')`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"000010_checkout_workflow.up.sql", "000011_refunds_returns.up.sql"} {
		sql, err := os.ReadFile("../../migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	var subtotal, shipping int64
	var state string
	if err := pool.QueryRow(ctx, `SELECT subtotal_amount, shipping_amount, checkout_state FROM orders WHERE id=$1`, order).Scan(&subtotal, &shipping, &state); err != nil {
		t.Fatal(err)
	}
	if subtotal != 20000 || shipping != 5000 || state != "ready" {
		t.Fatalf("unexpected order backfill %d %d %s", subtotal, shipping, state)
	}
	var source string
	var completedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT commission_source, completed_at FROM vendor_orders WHERE id=$1`, vo).Scan(&source, &completedAt); err != nil {
		t.Fatal(err)
	}
	if source != "payment_time_legacy" || completedAt == nil {
		t.Fatalf("legacy commission must be labelled and completion kept, got %s %v", source, completedAt)
	}
	var status, policy string
	var qty, amount int64
	if err := pool.QueryRow(ctx, `SELECT status, quantity, refund_amount, policy_version FROM return_requests WHERE id=$1`, rr).Scan(&status, &qty, &amount, &policy); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || qty != 2 || amount != 20000 || policy != "legacy" {
		t.Fatalf("a legacy approval must not become refunded, got %s %d %d %s", status, qty, amount, policy)
	}
	var events, refunds int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM return_request_events WHERE return_request_id=$1`, rr).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM order_refunds`).Scan(&refunds)
	if events != 1 || refunds != 0 {
		t.Fatalf("expected one migration audit entry and no fabricated refund, got %d / %d", events, refunds)
	}
}

func itemsVendor(items []*domain.OrderItem) string { return items[0].VendorOrderID }
func ptrStr(s string) *string                      { return &s }
func ptrInt(i int) *int                            { return &i }

func TestReturnsAllowOneOpenRequestPerItemAndCountReturnedUnits(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, seedPlan(t, pool, buyer))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := repository.NewOrderRepository(pool).ListItemsByOrder(ctx, order.ID)
	returns := repository.NewReturnRequestRepository(pool)
	newReturn := func(qty int64) *domain.ReturnRequest {
		return &domain.ReturnRequest{OrderID: order.ID, OrderItemID: items[0].ID, BuyerID: buyer, Reason: "r", Quantity: qty,
			RefundAmount: qty * items[0].PriceAmount, PolicyVersion: "window-7d", ReturnWindowDays: ptrInt(7)}
	}
	first := newReturn(1)
	if err := returns.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := returns.Create(ctx, newReturn(1)); err == nil {
		t.Fatal("a second open request for the item must be refused")
	}
	if err := returns.Transition(ctx, first, domain.ReturnRejected, repository.ReturnUpdate{DecidedBy: ptrStr(uuid.NewString())}); err != nil {
		t.Fatal(err)
	}
	if n, err := returns.ReturnedQuantity(ctx, items[0].ID); err != nil || n != 0 {
		t.Fatalf("rejected units must not count, got %d %v", n, err)
	}
	if err := returns.Create(ctx, newReturn(2)); err != nil {
		t.Fatalf("after a rejection the item may be returned again: %v", err)
	}
	if n, _ := returns.ReturnedQuantity(ctx, items[0].ID); n != 2 {
		t.Fatalf("expected 2 returned units, got %d", n)
	}
}

func TestSettlementHoldsAndEffectKind(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	order, err := repository.NewOrderRepository(pool).CreateFromPlan(ctx, seedPlan(t, pool, buyer))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := repository.NewOrderRepository(pool).ListItemsByOrder(ctx, order.ID)
	vo := itemsVendor(items)
	vendorOrders := repository.NewVendorOrderRepository(pool)
	if held, err := vendorOrders.HeldForSettlement(ctx, []string{vo}); err != nil || len(held) != 0 {
		t.Fatalf("nothing open: %v %v", held, err)
	}
	returns := repository.NewReturnRequestRepository(pool)
	rr := &domain.ReturnRequest{OrderID: order.ID, OrderItemID: items[0].ID, BuyerID: buyer, Reason: "r", Quantity: 1,
		RefundAmount: items[0].PriceAmount, PolicyVersion: "window-7d", ReturnWindowDays: ptrInt(7)}
	if err := returns.Create(ctx, rr); err != nil {
		t.Fatal(err)
	}
	if held, _ := vendorOrders.HeldForSettlement(ctx, []string{vo}); held[vo] != "return_open" {
		t.Fatalf("an open return must hold the vendor order, got %v", held)
	}
	if err := returns.Transition(ctx, rr, domain.ReturnRejected, repository.ReturnUpdate{DecidedBy: ptrStr(uuid.NewString())}); err != nil {
		t.Fatal(err)
	}
	refund := &domain.Refund{OrderID: order.ID, VendorOrderID: &vo, ReasonCode: "dispute", Amount: 100, Currency: "VND", Reason: "r", RequestedBy: uuid.NewString()}
	if err := repository.NewRefundRepository(pool).Create(ctx, refund); err != nil {
		t.Fatal(err)
	}
	if held, _ := vendorOrders.HeldForSettlement(ctx, []string{vo, uuid.NewString()}); len(held) != 1 || held[vo] != "refund_open" {
		t.Fatalf("an open refund must hold the vendor order, got %v", held)
	}
	e := domain.Effect{OrderID: order.ID, Kind: domain.EffectSettleVendorOrder, Target: vo}
	if err := repository.NewEffectRepository(pool).Enqueue(ctx, e); err != nil {
		t.Fatalf("migration 000012 must allow settlement effects: %v", err)
	}
}
