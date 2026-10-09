package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"
)

// returnDestinations stands in for Vendor: the verified destination, or
// none.
type returnDestinations struct {
	mu   sync.Mutex
	dest *domain.ReturnDestination
}

func (r *returnDestinations) set(d *domain.ReturnDestination) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dest = d
}

func (r *returnDestinations) ReturnDestination(context.Context, string) (*domain.ReturnDestination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dest, nil
}

// returnParcels stands in for Shipment's return shipments.
type returnParcels struct {
	mu         sync.Mutex
	authorized []adapter.ReturnShipmentAuthorization
	dispatched []string
	received   []string
	exceptions []string
	id         string
}

func (p *returnParcels) AuthorizeReturnShipment(_ context.Context, a adapter.ReturnShipmentAuthorization) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorized = append(p.authorized, a)
	if p.id == "" {
		p.id = uuid.NewString()
	}
	return p.id, nil
}

func (p *returnParcels) DispatchReturnShipment(_ context.Context, id, _, _, tracking string, _ time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dispatched = append(p.dispatched, id+"/"+tracking)
	return nil
}

func (p *returnParcels) ReceiveReturnShipment(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.received = append(p.received, id)
	return nil
}

func (p *returnParcels) ReturnShipmentException(_ context.Context, id, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exceptions = append(p.exceptions, id)
	return nil
}

// restockRecorder records the restocked quantity per return.
type restockRecorder struct {
	inventoryReceiptStub
	mu   sync.Mutex
	done map[string]int64
}

func (r *restockRecorder) RestockReturn(_ context.Context, id, _ string, _ *string, qty int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done[id] = qty
	return nil
}

type returnFixture struct {
	uc                  *usecase.OrderUseCase
	dests               *returnDestinations
	parcels             *returnParcels
	stock               *restockRecorder
	buyer, admin, order string
	vendorOrder, vendor string
	vendorUser, item    string
}

func destination(version int64, province string) *domain.ReturnDestination {
	return &domain.ReturnDestination{RecipientName: "Kho trả hàng", Phone: "0900000001", Province: province, District: "Quận 1", Ward: "Bến Nghé",
		StreetAddress: "1 Đường Kho", ReceivingHours: "8:00-17:00 T2-T6", VendorAddressID: uuid.NewString(), DestinationVersion: version}
}

func newReturnFixture(t *testing.T, pool *pgxpool.Pool) *returnFixture {
	t.Helper()
	uc, _ := holdUseCase(pool, "ok")
	f := &returnFixture{uc: uc, dests: &returnDestinations{}, parcels: &returnParcels{}, stock: &restockRecorder{done: map[string]int64{}},
		buyer: uuid.NewString(), admin: uuid.NewString(), vendorUser: uuid.NewString(), item: uuid.NewString()}
	f.order, f.vendorOrder, f.vendor = paidSupportOrder(t, pool, f.buyer)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(id,order_id,vendor_order_id,product_id,product_name,price_amount,quantity,subtotal_amount)
		VALUES($1,$2,$3,gen_random_uuid(),'Test product',50,2,100)`, f.item, f.order, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE vendor_orders SET status = 'completed', completed_at = now() WHERE id = $1`, f.vendorOrder); err != nil {
		t.Fatal(err)
	}
	uc.ReturnPolicy = domain.ReturnPolicy{Version: "returns-v1", WindowDays: 7}
	uc.ReturnShipping, uc.ReturnDestinations, uc.ReturnParcels, uc.ReturnShippingEnabled = repository.NewReturnRequestRepository(pool), f.dests, f.parcels, true
	uc.Inventory = f.stock
	uc.Vendors = shopOwner{user: f.vendorUser, vendor: f.vendor}
	return f
}

func (f *returnFixture) approvedReturn(t *testing.T) *domain.ReturnRequest {
	t.Helper()
	rr, err := f.uc.CreateReturn(t.Context(), f.buyer, usecase.ReturnInput{OrderID: f.order, ItemID: f.item, Quantity: 2, Reason: "Sai kích cỡ"})
	if err != nil {
		t.Fatal(err)
	}
	rr, err = f.uc.AdminDecideReturnWithTerms(t.Context(), f.admin, rr.ID, true, "Đồng ý", usecase.ReturnShippingTerms{})
	if err != nil {
		t.Fatal(err)
	}
	return rr
}

func (f *returnFixture) load(t *testing.T, id string) *domain.ReturnRequest {
	t.Helper()
	rr, err := f.uc.GetAdminReturn(t.Context(), f.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	return rr
}

func (f *returnFixture) dispatch(ctx context.Context, rr *domain.ReturnRequest, key, tracking string) (*domain.ReturnRequest, bool, error) {
	return f.uc.ReportReturnDispatch(ctx, f.buyer, rr.ID, usecase.ReturnDispatchInput{CarrierName: "GHN", TrackingNumber: tracking,
		DispatchedAt: time.Now().Add(-time.Minute).Truncate(time.Second), ExpectedVersion: rr.Version, IdempotencyKey: key})
}

// AF-05: approval without a verified destination waits for one (nothing
// is taken from the shop's current address); the instructions snapshot
// the verified destination and may be corrected only before dispatch; the
// buyer's dispatch is idempotent; the goods receipt (all sellable)
// restocks and refunds once; the parcel is opened, sent and closed at
// Shipment.
func TestReturnShippingFromAuthorizationToRefund(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)

	rr := f.approvedReturn(t)
	if rr.ShippingStatus == nil || *rr.ShippingStatus != domain.ShippingDestinationMissing || rr.Authorized() {
		t.Fatalf("no verified destination: the return waits for one: %+v", rr)
	}
	if _, err := f.uc.GetShippingInstructions(ctx, f.buyer, rr.ID); code(err) != domain.CodeReturnNotApproved {
		t.Fatalf("no instructions yet: %v", err)
	}
	if _, err := f.uc.AuthorizeReturnShipping(ctx, f.admin, rr.ID, usecase.AuthorizeReturnShippingInput{Reason: "Shop đã có địa chỉ", ExpectedVersion: rr.Version}); code(err) != domain.CodeNoReturnDest {
		t.Fatalf("authorizing needs a verified destination: %v", err)
	}
	f.dests.set(destination(1, "79"))
	if _, err := f.uc.AuthorizeReturnShipping(ctx, f.admin, rr.ID, usecase.AuthorizeReturnShippingInput{Reason: "x", ExpectedVersion: rr.Version - 1}); code(err) != domain.CodeVersionConflict {
		t.Fatalf("stale version: %v", err)
	}
	rr, err := f.uc.AuthorizeReturnShipping(ctx, f.admin, rr.ID, usecase.AuthorizeReturnShippingInput{Reason: "Shop đã có địa chỉ", ExpectedVersion: rr.Version})
	if err != nil || rr.AuthorizationVersion != 1 || *rr.ShippingStatus != domain.ShippingAwaitingDispatch || *rr.FeePayer != domain.FeePayerSeller ||
		rr.DispatchDeadline == nil || rr.DispatchDeadline.Sub(*rr.AuthorizedAt) != 7*24*time.Hour {
		t.Fatalf("authorized with a 7-day deadline, seller pays by default: %+v %v", rr, err)
	}
	runDue(t, pool, f.uc)
	if len(f.parcels.authorized) != 1 || f.parcels.authorized[0].AuthorizationVersion != 1 || f.parcels.authorized[0].Destination.Province != "79" {
		t.Fatalf("Shipment opens the parcel once: %+v", f.parcels.authorized)
	}
	if current := f.load(t, rr.ID); current.Version != rr.Version || current.ReturnShipmentID == nil {
		t.Fatalf("the parcel id is recorded without making the buyer's view stale: %d/%d %+v", current.Version, rr.Version, current.ReturnShipmentID)
	}

	// The shop's destination is corrected and verified again before dispatch.
	f.dests.set(destination(2, "01"))
	rr, err = f.uc.AuthorizeReturnShipping(ctx, f.admin, rr.ID, usecase.AuthorizeReturnShippingInput{Reason: "Đổi kho nhận", ExpectedVersion: rr.Version})
	if err != nil || rr.AuthorizationVersion != 2 {
		t.Fatalf("re-authorized before dispatch: %+v %v", rr, err)
	}
	runDue(t, pool, f.uc)
	if len(f.parcels.authorized) != 2 || f.parcels.authorized[1].Destination.Province != "01" {
		t.Fatalf("the parcel follows the new authorization: %+v", f.parcels.authorized)
	}
	instructions, err := f.uc.GetShippingInstructions(ctx, f.buyer, rr.ID)
	if err != nil || instructions.Return.Destination.Province != "01" || instructions.ReturnCode == "" {
		t.Fatalf("instructions: %+v %v", instructions, err)
	}
	if _, err := f.uc.GetShippingInstructions(ctx, uuid.NewString(), rr.ID); err == nil {
		t.Fatal("another buyer cannot read the instructions")
	}

	if _, _, err := f.dispatch(ctx, rr, "", "VN0001"); err == nil {
		t.Fatal("dispatch needs an Idempotency-Key")
	}
	if _, _, err := f.dispatch(ctx, rr, "dispatch-key-1", "x"); code(err) != domain.CodeInvalidTracking {
		t.Fatalf("invalid tracking: %v", err)
	}
	if _, _, err := f.uc.ReportReturnDispatch(ctx, f.buyer, rr.ID, usecase.ReturnDispatchInput{CarrierName: "GHN", TrackingNumber: "VN0001",
		DispatchedAt: time.Now().Add(24 * time.Hour), ExpectedVersion: rr.Version, IdempotencyKey: "dispatch-key-1"}); err == nil {
		t.Fatal("a dispatch in the future is refused")
	}
	sent, replayed, err := f.dispatch(ctx, rr, "dispatch-key-1", "VN0001")
	if err != nil || replayed || *sent.ShippingStatus != domain.ShippingAwaitingVerification {
		t.Fatalf("dispatch: %+v %v %v", sent, replayed, err)
	}
	if _, replayed, err := f.dispatch(ctx, rr, "dispatch-key-1", "VN0001"); err != nil || !replayed {
		t.Fatalf("a retried dispatch is a replay: %v %v", replayed, err)
	}
	if _, _, err := f.dispatch(ctx, rr, "dispatch-key-1", "VN0002"); code(err) != domain.CodeSupportKeyReused {
		t.Fatalf("the same key with another tracking number: %v", err)
	}
	f.dests.set(destination(3, "48"))
	if _, err := f.uc.AuthorizeReturnShipping(ctx, f.admin, rr.ID, usecase.AuthorizeReturnShippingInput{Reason: "x", ExpectedVersion: sent.Version}); code(err) != domain.CodeDestinationChanged {
		t.Fatalf("no new destination after dispatch: %v", err)
	}
	runDue(t, pool, f.uc)
	if len(f.parcels.dispatched) != 1 {
		t.Fatalf("the dispatch reaches Shipment once: %+v", f.parcels.dispatched)
	}
	if _, err := f.uc.MarkReturnReceived(ctx, f.vendorUser, "vendor", rr.ID, true, ""); err == nil {
		t.Fatal("an authorized return is received with a goods receipt")
	}

	current := f.load(t, rr.ID)
	receipt := func(ctx context.Context, user string, sellable, damaged, missing int64) (*domain.ReturnRequest, error) {
		return f.uc.RecordReturnReceipt(ctx, usecase.SupportActor{ID: user, Role: "vendor"}, rr.ID,
			usecase.ReturnReceiptInput{Sellable: sellable, Damaged: damaged, Missing: missing, ExpectedVersion: current.Version})
	}
	if _, err := receipt(ctx, f.vendorUser, 3, 0, 0); err == nil {
		t.Fatal("never more units than approved")
	}
	if _, err := receipt(ctx, uuid.NewString(), 2, 0, 0); err == nil {
		t.Fatal("another shop cannot receive the goods")
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := receipt(context.Background(), f.vendorUser, 2, 0, 0); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("two people receiving at once: one wins, got %d", wins.Load())
	}
	received := f.load(t, rr.ID)
	if received.Status != domain.ReturnRefundPending || *received.ShippingStatus != domain.ShippingReceived || received.InspectionDisputed {
		t.Fatalf("all sellable: refund requested: %+v", received)
	}
	runDue(t, pool, f.uc)
	if f.stock.done[rr.ID] != 2 || len(f.parcels.received) != 1 {
		t.Fatalf("restocked the sellable units once, parcel closed: %+v %+v", f.stock.done, f.parcels.received)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_refunds WHERE return_request_id = $1 AND amount = 100`, rr.ID); n != 1 {
		t.Fatalf("one refund of the returned units, got %d", n)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_admin_audit WHERE entity_id = $1 AND action = 'return_shipping_authorized'`, rr.ID); n != 2 {
		t.Fatalf("both authorizations are audited, got %d", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM return_goods_receipts WHERE return_id = $1`, rr.ID); err == nil {
		t.Fatal("receipts are append-only")
	}
}

// AF-05: damaged or missing goods are not silently deducted: sellable
// units go back to stock, the refund waits for an admin who refunds in
// full (or handles a dispute in a support case).
func TestDisputedReturnReceiptWaitsForAnAdmin(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	f.dests.set(destination(1, "79"))
	rr := f.approvedReturn(t)
	if !rr.Authorized() {
		t.Fatalf("a verified destination authorizes on approval: %+v", rr)
	}
	runDue(t, pool, f.uc)
	rr = f.load(t, rr.ID)
	got, err := f.uc.RecordReturnReceipt(ctx, usecase.SupportActor{ID: f.vendorUser, Role: "vendor"}, rr.ID,
		usecase.ReturnReceiptInput{Sellable: 1, Damaged: 1, Note: "Một chiếc bị vỡ", ExpectedVersion: rr.Version})
	if err != nil || got.Status != domain.ReturnReceived || !got.InspectionDisputed {
		t.Fatalf("disputed: received, no refund: %+v %v", got, err)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM order_refunds WHERE return_request_id = $1`, rr.ID); n != 0 {
		t.Fatal("no refund before an admin decides")
	}
	runDue(t, pool, f.uc)
	if f.stock.done[rr.ID] != 1 {
		t.Fatalf("only the sellable unit is restocked: %+v", f.stock.done)
	}
	got, err = f.uc.DecideReturnShipping(ctx, f.admin, rr.ID, usecase.ReturnShippingDecision{Action: "refund", Reason: "Hỏng do vận chuyển", ExpectedVersion: got.Version})
	if err != nil || got.Status != domain.ReturnRefundPending {
		t.Fatalf("admin refunds in full: %+v %v", got, err)
	}
}

// AF-05: a parcel not sent by the deadline puts the return in review once;
// the buyer can still send it. A parcel lost on the way back is marked by
// an admin and closed at Shipment.
func TestOverdueAndLostReturnParcels(t *testing.T) {
	pool := orderDB(t)
	ctx := t.Context()
	f := newReturnFixture(t, pool)
	f.dests.set(destination(1, "79"))
	rr := f.approvedReturn(t)
	runDue(t, pool, f.uc)
	if _, err := pool.Exec(ctx, `UPDATE return_requests SET dispatch_deadline = now() - interval '1 hour' WHERE id = $1`, rr.ID); err != nil {
		t.Fatal(err)
	}
	f.uc.FlagOverdueReturns(ctx)
	f.uc.FlagOverdueReturns(ctx)
	late := f.load(t, rr.ID)
	if late.DispatchOverdueAt == nil || late.Status != domain.ReturnApproved || late.WaitingOn != "admin" {
		t.Fatalf("overdue goes to review, the return stays approved: %+v", late)
	}
	if n := countRowsIn(t, pool, `SELECT count(*) FROM return_request_events WHERE return_request_id = $1 AND action = 'dispatch_overdue'`, rr.ID); n != 1 {
		t.Fatalf("flagged once, got %d", n)
	}
	sent, _, err := f.dispatch(ctx, late, "dispatch-key-late", "VN0009")
	if err != nil || *sent.ShippingStatus != domain.ShippingAwaitingVerification {
		t.Fatalf("a late parcel is still accepted: %+v %v", sent, err)
	}
	runDue(t, pool, f.uc)
	lost, err := f.uc.DecideReturnShipping(ctx, f.admin, rr.ID, usecase.ReturnShippingDecision{Action: "mark_lost", Reason: "Hãng xác nhận thất lạc", ExpectedVersion: sent.Version})
	if err != nil || *lost.ShippingStatus != domain.ShippingLost {
		t.Fatalf("lost: %+v %v", lost, err)
	}
	runDue(t, pool, f.uc)
	if len(f.parcels.exceptions) != 1 {
		t.Fatalf("the parcel closes as an exception at Shipment: %+v", f.parcels.exceptions)
	}
	if _, err := f.uc.RecordReturnReceipt(ctx, usecase.SupportActor{ID: f.vendorUser, Role: "vendor"}, rr.ID,
		usecase.ReturnReceiptInput{Sellable: 2, ExpectedVersion: lost.Version}); err != nil {
		t.Fatalf("goods turning up after all can still be received: %v", err)
	}
}
