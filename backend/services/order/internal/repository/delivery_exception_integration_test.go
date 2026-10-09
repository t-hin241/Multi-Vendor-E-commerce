package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// replacementShipment stands in for Shipment's redelivery attempts: one
// shipment per operation id.
type replacementShipment struct {
	mu     sync.Mutex
	calls  []adapter.ReplacementAttempt
	byOp   map[string]string
	refuse bool
}

func (r *replacementShipment) CreateReplacementAttempt(_ context.Context, a adapter.ReplacementAttempt) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, a)
	if r.refuse {
		return "", domain.ShippingUnavailable("This order already has a delivery attempt in progress")
	}
	if id, ok := r.byOp[a.OperationID]; ok {
		return id, nil
	}
	id := uuid.NewString()
	r.byOp[a.OperationID] = id
	return id, nil
}

type deliveryFixture struct {
	uc                  *usecase.OrderUseCase
	payment             *holdPayment
	shipments           *replacementShipment
	stock               *recoveryStock
	buyer, admin, order string
	vendorOrder, vendor string
	vendorUser, item    string
	shipment            string
}

func newDeliveryFixture(t *testing.T, pool *pgxpool.Pool) *deliveryFixture {
	t.Helper()
	uc, payment := holdUseCase(pool, "ok")
	f := &deliveryFixture{uc: uc, payment: payment, shipments: &replacementShipment{byOp: map[string]string{}}, stock: &recoveryStock{done: map[string]int64{}},
		buyer: uuid.NewString(), admin: uuid.NewString(), vendorUser: uuid.NewString(), item: uuid.NewString(), shipment: uuid.NewString()}
	f.order, f.vendorOrder, f.vendor = paidSupportOrder(t, pool, f.buyer)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(id,order_id,vendor_order_id,product_id,product_name,price_amount,quantity,subtotal_amount)
		VALUES($1,$2,$3,gen_random_uuid(),'Test product',50,2,100)`, f.item, f.order, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE vendor_orders SET status = 'shipped' WHERE id = $1`, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET status = 'shipped' WHERE id = $1`, f.order); err != nil {
		t.Fatal(err)
	}
	uc.DeliveryExceptions, uc.Replacements, uc.Recoveries, uc.DeliveryRedelivery = repository.DeliveryExceptionRepository{Pool: pool}, f.shipments, f.stock, true
	uc.Vendors = shopOwner{user: f.vendorUser, vendor: f.vendor}
	return f
}

func (f *deliveryFixture) fact(ctx context.Context, shipment, kind string, attempt int) error {
	return f.uc.ApplyShipmentException(ctx, usecase.ShipmentExceptionFact{EventID: uuid.NewString(), ShipmentID: shipment, VendorOrderID: f.vendorOrder,
		Type: kind, AttemptNo: attempt, FailedAttempts: 2, Reason: "Không liên lạc được người nhận"})
}

func (f *deliveryFixture) open(t *testing.T) *domain.DeliveryException {
	t.Helper()
	list, err := f.uc.ListDeliveryExceptions(t.Context(), "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.VendorOrderID == f.vendorOrder {
			detail, err := f.uc.GetDeliveryException(t.Context(), usecase.SupportActor{ID: f.admin, Role: "admin"}, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			return detail.Exception
		}
	}
	t.Fatal("no delivery exception for the vendor order")
	return nil
}

func (f *deliveryFixture) receipt(ctx context.Context, role, actor string, d *domain.DeliveryException, lines ...domain.ReceiptLine) (*domain.DeliveryException, error) {
	note := ""
	if role == "admin" {
		note = "Kiểm lại kiện hàng"
	}
	return f.uc.RecordGoodsReceipt(ctx, usecase.SupportActor{ID: actor, Role: role}, d.ID, usecase.ReceiptInput{Lines: lines, Note: note, ExpectedVersion: d.Version})
}

// AF-04: duplicate and out-of-order facts make one case; the payout is
// held; the shop accounts for every unit; the refund includes the package
// and only sellable units go back to stock, once; the buyer is told the
// money is back only after Payment confirms.
func TestReturnedPackageOneCaseReceiptRefundRestockOnce(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	f.payment.setMode("down")

	if err := f.fact(ctx, f.shipment, domain.FactAttemptsExhausted, 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f.fact(context.Background(), f.shipment, domain.FactReturned, 1); err != nil {
				t.Errorf("returned fact: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := countRowsIn(t, pool, `SELECT count(*) FROM delivery_exceptions WHERE vendor_order_id = $1`, f.vendorOrder); n != 1 {
		t.Fatalf("one case per package, got %d", n)
	}
	d := f.open(t)
	if d.Status != domain.DXAwaitingGoods || d.CarrierOutcome != domain.FactReturned || d.ExceptionType != domain.FactAttemptsExhausted ||
		*d.HoldStatus != domain.HoldPreparing {
		t.Fatalf("returned after the attempt limit waits for the goods: %+v", d)
	}
	held, err := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{f.vendorOrder})
	if err != nil || held[f.vendorOrder] == "" {
		t.Fatalf("an open case holds the payout: %v %v", held, err)
	}
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, uuid.NewString()); code(err) != domain.CodeDeliveryExceptionOpen {
		t.Fatalf("no new handover while the case is open: %v", err)
	}

	if _, err := f.receipt(ctx, "vendor", f.vendorUser, d, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 1}); err == nil {
		t.Fatal("every unit must be accounted for")
	}
	if _, err := f.receipt(ctx, "vendor", uuid.NewString(), d, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2}); err == nil {
		t.Fatal("another shop cannot record the goods")
	}
	d, err = f.receipt(ctx, "vendor", f.vendorUser, d,
		domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 1},
		domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionDamaged, Quantity: 1})
	if err != nil || d.Receipt == nil || d.Receipt.AllSellable() {
		t.Fatalf("receipt: %+v %v", d, err)
	}
	if _, err := f.receipt(ctx, "vendor", f.vendorUser, d, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2}); code(err) != domain.CodeReceiptExists {
		t.Fatalf("the shop records the goods once: %v", err)
	}
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "redeliver", Reason: "Giao lại", ExpectedVersion: d.Version}); err == nil {
		t.Fatal("damaged goods are not redelivered")
	}
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "refund", Reason: "Hoàn tiền", ExpectedVersion: d.Version}); code(err) != domain.CodeHoldUnavailable {
		t.Fatalf("no refund before the hold is confirmed: %v", err)
	}
	f.payment.setMode("ok")
	runDue(t, pool, f.uc)
	d = f.open(t)
	if *d.HoldStatus != domain.HoldActive {
		t.Fatalf("hold confirmed: %+v", d)
	}
	d, err = f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "refund", Reason: "Hàng hoàn về, hoàn tiền", ExpectedVersion: d.Version})
	if err != nil || d.Status != domain.DXRefundPending || d.RefundID == nil {
		t.Fatalf("refund: %+v %v", d, err)
	}
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "redeliver", Reason: "Giao lại", ExpectedVersion: d.Version}); code(err) != domain.CodeResolutionLocked {
		t.Fatalf("after a refund no redelivery: %v", err)
	}
	refund, err := repository.NewRefundRepository(pool).FindByID(ctx, *d.RefundID)
	if err != nil || refund.ReasonCode != domain.RefundReasonDeliveryException || refund.Amount != 100 {
		t.Fatalf("refund of the package: %+v %v", refund, err)
	}
	runDue(t, pool, f.uc)
	if len(f.stock.done) != 0 {
		t.Fatal("no stock goes back before the refund is confirmed")
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND kind = 'notify' AND target LIKE 'order_refunded:%'`, f.order); n != 0 {
		t.Fatal("the buyer is not told the money is back before Payment confirms")
	}
	if err := f.uc.ApplyRefundOutcome(ctx, domain.RefundOutcome{RefundID: refund.ID, PaymentRefundID: uuid.NewString(), Status: domain.RefundSucceeded,
		Amount: 100, Currency: "VND"}); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	runDue(t, pool, f.uc)
	detail, err := f.uc.GetDeliveryException(ctx, usecase.SupportActor{ID: f.admin, Role: "admin"}, d.ID)
	if err != nil || detail.Exception.Status != domain.DXResolved || *detail.Exception.HoldStatus != domain.HoldReleased || detail.Exception.RecoveryRef == nil {
		t.Fatalf("resolved, hold released, stock recovered: %+v %v", detail, err)
	}
	if len(f.stock.done) != 1 || f.stock.done[domain.DeliveryRecoveryID(d.ID, f.item)] != 1 {
		t.Fatalf("only the sellable unit goes back, once: %+v", f.stock.done)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_effects WHERE order_id = $1 AND kind = 'recover_delivery_stock'`, f.order); n != 1 {
		t.Fatalf("one restock effect, got %d", n)
	}
	vo, err := repository.NewVendorOrderRepository(pool).FindByID(ctx, f.vendorOrder)
	if err != nil || vo.Status != domain.StatusRefunded {
		t.Fatalf("the package is refunded after Payment confirmed: %+v %v", vo, err)
	}
	if _, err := f.receipt(ctx, "admin", f.admin, detail.Exception, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2}); err == nil {
		t.Fatal("no receipt correction after the stock went back")
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_admin_audit WHERE entity_type = 'delivery_exception' AND entity_id = $1`, d.ID); n != 1 {
		t.Fatalf("the decision is audited, got %d", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM delivery_exception_receipts WHERE exception_id = $1`, d.ID); err == nil {
		t.Fatal("receipts are append-only")
	}
	// A repeated fact after resolution opens nothing.
	if err := f.fact(ctx, f.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM delivery_exceptions WHERE vendor_order_id = $1`, f.vendorOrder); n != 1 {
		t.Fatalf("a late repeat opens no second case, got %d", n)
	}
}

// AF-04: a redelivery needs the buyer's consent (their own address), opens
// one new attempt at Shipment, only that attempt may be handed over, and
// its delivery resolves the case and releases the payout.
func TestRedeliveryNeedsConsentAndResolvesOnDelivery(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	if err := f.fact(ctx, f.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	d := f.open(t)
	d, err := f.receipt(ctx, "vendor", f.vendorUser, d, domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.uc.DeliveryRedelivery = false
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "redeliver", Reason: "Giao lại", ExpectedVersion: d.Version}); code(err) != domain.CodeRedeliveryOff {
		t.Fatalf("redelivery only with the flag: %v", err)
	}
	f.uc.DeliveryRedelivery = true
	d, err = f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "redeliver", Reason: "Khách hẹn lại", ExpectedVersion: d.Version})
	if err != nil || d.Status != domain.DXAwaitingBuyer {
		t.Fatalf("redelivery offered: %+v %v", d, err)
	}
	if _, err := f.uc.ConsentRedelivery(ctx, uuid.NewString(), d.ID, usecase.ConsentInput{Accept: true, ExpectedVersion: d.Version}); err == nil {
		t.Fatal("only the buyer answers")
	}
	other := uuid.NewString()
	var foreign, own string
	if err := pool.QueryRow(ctx, `INSERT INTO buyer_addresses(buyer_id,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,'Other','0900000000','79','1','1','Other street') RETURNING id`, other).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO buyer_addresses(buyer_id,recipient_name,phone,province,district,ward,street_address)
		VALUES($1,'Người nhận mới','0911111111','01','2','3','Số 5 phố mới') RETURNING id`, f.buyer).Scan(&own); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.ConsentRedelivery(ctx, f.buyer, d.ID, usecase.ConsentInput{Accept: true, AddressID: foreign, ExpectedVersion: d.Version}); err == nil {
		t.Fatal("another buyer's address is refused")
	}
	if _, err := f.uc.ConsentRedelivery(ctx, f.buyer, d.ID, usecase.ConsentInput{Accept: true, AddressID: own, ExpectedVersion: d.Version - 1}); code(err) != domain.CodeVersionConflict {
		t.Fatalf("a stale answer is refused: %v", err)
	}
	accepted, err := f.uc.ConsentRedelivery(ctx, f.buyer, d.ID, usecase.ConsentInput{Accept: true, AddressID: own, ExpectedVersion: d.Version})
	if err != nil || accepted.Status != domain.DXRedeliveryPending || accepted.RedeliveryAddress == nil || accepted.RedeliveryAddress.Province != "01" {
		t.Fatalf("consent with the new address: %+v %v", accepted, err)
	}
	runDue(t, pool, f.uc)
	runDue(t, pool, f.uc)
	if len(f.shipments.calls) != 1 || f.shipments.calls[0].AttemptNo != 2 || f.shipments.calls[0].OriginalShipmentID != f.shipment ||
		f.shipments.calls[0].Destination.StreetAddress != "Số 5 phố mới" {
		t.Fatalf("one replacement attempt for the agreed address: %+v", f.shipments.calls)
	}
	d = f.open(t)
	if d.ReplacementShipmentID == nil {
		t.Fatalf("the attempt is recorded: %+v", d)
	}
	replacement := *d.ReplacementShipmentID
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, uuid.NewString()); code(err) != domain.CodeDeliveryExceptionOpen {
		t.Fatalf("only the redelivery may be handed over: %v", err)
	}
	if err := f.uc.ClaimHandover(ctx, f.vendorOrder, replacement); err != nil {
		t.Fatalf("the redelivery is handed over: %v", err)
	}
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "refund", Reason: "Hoàn", ExpectedVersion: d.Version}); err == nil {
		t.Fatal("no refund while the redelivery is under way")
	}
	if err := f.uc.ApplyShipmentEvent(ctx, usecase.ShipmentEvent{EventID: uuid.NewString(), ShipmentID: replacement, VendorOrderID: f.vendorOrder, Type: "shipped"}); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.ApplyShipmentEvent(ctx, usecase.ShipmentEvent{EventID: uuid.NewString(), ShipmentID: replacement, VendorOrderID: f.vendorOrder, Type: "delivered"}); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	detail, err := f.uc.GetDeliveryException(ctx, usecase.SupportActor{ID: f.buyer, Role: "buyer"}, d.ID)
	if err != nil || detail.Exception.Status != domain.DXResolved || detail.Exception.HoldStatus != nil || detail.Exception.Receipt != nil {
		t.Fatalf("resolved; the buyer sees no hold or receipt: %+v %v", detail, err)
	}
	if h := f.payment.released[*d.HoldID]; h.OperationID == "" {
		t.Fatal("the hold is released after the redelivery")
	}
	vo, _ := repository.NewVendorOrderRepository(pool).FindByID(ctx, f.vendorOrder)
	if vo.Status != domain.StatusCompleted {
		t.Fatalf("the redelivered package completes the order: %s", vo.Status)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_refunds WHERE vendor_order_id = $1`, f.vendorOrder); n != 0 {
		t.Fatal("a redelivery charges and refunds nothing")
	}
	if len(f.stock.done) != 0 {
		t.Fatal("redelivered goods are not restocked")
	}
}

// AF-04: refund and redelivery decided at once: one wins under the order
// lock; the other is refused.
func TestRefundAndRedeliveryRaceOneWins(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	if err := f.fact(ctx, f.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	d, err := f.receipt(ctx, "vendor", f.vendorUser, f.open(t), domain.ReceiptLine{OrderItemID: f.item, Condition: domain.ConditionSellable, Quantity: 2})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	for _, resolution := range []string{"refund", "redeliver", "refund", "redeliver"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.uc.DecideDeliveryException(context.Background(), f.admin, d.ID,
				usecase.DeliveryDecision{Resolution: resolution, Reason: "Quyết định", ExpectedVersion: d.Version}); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("exactly one decision wins, got %d", wins.Load())
	}
	d = f.open(t)
	refunds := countRowsIn(t, pool, `SELECT count(*) FROM order_refunds WHERE vendor_order_id = $1`, f.vendorOrder)
	switch d.Status {
	case domain.DXRefundPending:
		if refunds != 1 {
			t.Fatalf("one refund, got %d", refunds)
		}
	case domain.DXAwaitingBuyer:
		if refunds != 0 {
			t.Fatal("a redelivery creates no refund")
		}
	default:
		t.Fatalf("unexpected status %s", d.Status)
	}
}

// AF-04: a package that only exhausted its attempts may still arrive: no
// refund then; a late delivery puts the case in review (the payout stays
// held) and an admin closes it. A fact for a refunded package opens nothing.
func TestLateDeliveryGoesToReviewAndHoldsPayout(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newDeliveryFixture(t, pool)
	if err := f.fact(ctx, f.shipment, domain.FactAttemptsExhausted, 1); err != nil {
		t.Fatal(err)
	}
	runDue(t, pool, f.uc)
	d := f.open(t)
	if d.Status != domain.DXInvestigating {
		t.Fatalf("investigating: %+v", d)
	}
	if _, err := f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "refund", Reason: "Hoàn", ExpectedVersion: d.Version}); err == nil {
		t.Fatal("no refund while the carrier may still deliver")
	}
	if err := f.uc.ApplyShipmentEvent(ctx, usecase.ShipmentEvent{EventID: uuid.NewString(), ShipmentID: f.shipment, VendorOrderID: f.vendorOrder, Type: "delivered"}); err != nil {
		t.Fatal(err)
	}
	d = f.open(t)
	if d.Status != domain.DXNeedsReview || d.LateDeliveryAt == nil || d.CarrierOutcome != domain.OutcomeDelivered {
		t.Fatalf("late delivery goes to review: %+v", d)
	}
	held, err := repository.NewVendorOrderRepository(pool).HeldForSettlement(ctx, []string{f.vendorOrder})
	if err != nil || held[f.vendorOrder] != "delivery_exception_open" {
		t.Fatalf("the payout stays held during review: %v %v", held, err)
	}
	d, err = f.uc.DecideDeliveryException(ctx, f.admin, d.ID, usecase.DeliveryDecision{Resolution: "close", Reason: "Khách xác nhận đã nhận", ExpectedVersion: d.Version})
	if err != nil || d.Status != domain.DXResolved {
		t.Fatalf("closed: %+v %v", d, err)
	}
	runDue(t, pool, f.uc)
	if h := f.payment.released[*d.HoldID]; h.OperationID == "" {
		t.Fatal("the hold is released on close")
	}

	// A refunded package: the fact is acknowledged, no case opens.
	g := newDeliveryFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE vendor_orders SET status = 'refunded' WHERE id = $1`, g.vendorOrder); err != nil {
		t.Fatal(err)
	}
	if err := g.fact(ctx, g.shipment, domain.FactReturned, 1); err != nil {
		t.Fatal(err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM delivery_exceptions WHERE vendor_order_id = $1`, g.vendorOrder); n != 0 {
		t.Fatal("no case for a refunded package")
	}
}
