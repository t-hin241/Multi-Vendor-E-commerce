package usecase_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

type fakeOrderRepository struct {
	mu           sync.Mutex
	byID         map[string]*domain.Order
	items        map[string][]*domain.OrderItem
	nextID       int
	vendorOrders *fakeVendorOrderRepository
	consumptions *fakeCartConsumptionRepository
}

// newFakeOrderRepository takes the same fakeVendorOrderRepository the use
// case is wired with, since CreateFromPlan inserts an order's vendor
// sub-orders in the same transaction as the order itself in the real
// repository — the fake mirrors that by writing into the shared store.
func newFakeOrderRepository(vendorOrders *fakeVendorOrderRepository) *fakeOrderRepository {
	return &fakeOrderRepository{byID: make(map[string]*domain.Order), items: make(map[string][]*domain.OrderItem), vendorOrders: vendorOrders}
}

func (f *fakeOrderRepository) CreateFromPlan(_ context.Context, plan *domain.Plan) (*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++
	order := plan.Order
	order.ID = "order-" + strconv.Itoa(f.nextID)
	f.byID[order.ID] = &order

	vendorOrderIDs := make([]string, len(plan.VendorOrders))
	for i, vo := range plan.VendorOrders {
		id := "vo-" + strconv.Itoa(f.nextID) + "-" + strconv.Itoa(i)
		vendorOrderIDs[i] = id
		voCopy := vo
		voCopy.ID = id
		voCopy.OrderID = order.ID
		f.vendorOrders.create(&voCopy)
	}

	for _, item := range plan.Items {
		idx, _ := strconv.Atoi(item.VendorOrderID)
		itemCopy := item
		itemCopy.OrderID = order.ID
		itemCopy.VendorOrderID = vendorOrderIDs[idx]
		f.items[order.ID] = append(f.items[order.ID], &itemCopy)
		f.vendorOrders.addItem(&itemCopy)
	}

	if plan.CartConsumption != nil && f.consumptions != nil {
		f.consumptions.insert(order.ID, plan.CartConsumption)
	}

	return &order, nil
}

func (f *fakeOrderRepository) FindByID(_ context.Context, id string) (*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrOrderNotFound
	}
	copyO := *o
	return &copyO, nil
}

func (f *fakeOrderRepository) ListByBuyer(_ context.Context, buyerID string, _, _ int) ([]*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Order
	for _, o := range f.byID {
		if o.BuyerID == buyerID {
			copyO := *o
			out = append(out, &copyO)
		}
	}
	return out, nil
}

func (f *fakeOrderRepository) UpdateStatus(_ context.Context, id string, status domain.Status, reason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok {
		return repository.ErrOrderNotFound
	}
	o.Status = status
	o.CancellationReason = reason
	return nil
}

func (f *fakeOrderRepository) ListItemsByOrder(_ context.Context, orderID string) ([]*domain.OrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[orderID], nil
}

func (f *fakeOrderRepository) ListReviewEligibility(_ context.Context, buyerID, productID string) ([]*domain.ReviewEligibility, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.ReviewEligibility
	for orderID, order := range f.byID {
		if order.BuyerID != buyerID {
			continue
		}
		for _, item := range f.items[orderID] {
			if productID != "" && item.ProductID != productID {
				continue
			}
			vo, err := f.vendorOrders.FindByID(context.Background(), item.VendorOrderID)
			if err != nil || vo.Status != domain.StatusCompleted {
				continue
			}
			out = append(out, &domain.ReviewEligibility{OrderItemID: item.ID, VendorOrderID: item.VendorOrderID, ProductID: item.ProductID, VendorID: vo.VendorID, ProductName: item.ProductName, VariantLabel: item.VariantLabel, CompletedAt: vo.UpdatedAt})
		}
	}
	return out, nil
}

func (f *fakeOrderRepository) ListByStatus(_ context.Context, status string, _, _ int) ([]*domain.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Order
	for _, o := range f.byID {
		if status == "" || string(o.Status) == status {
			cp := *o
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeOrderRepository) UpdateTotalAmount(_ context.Context, id string, totalAmount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok {
		return repository.ErrOrderNotFound
	}
	o.TotalAmount = totalAmount
	return nil
}

type fakeVendorOrderRepository struct {
	mu    sync.Mutex
	byID  map[string]*domain.VendorOrder
	items map[string][]*domain.OrderItem
}

func newFakeVendorOrderRepository() *fakeVendorOrderRepository {
	return &fakeVendorOrderRepository{byID: make(map[string]*domain.VendorOrder), items: make(map[string][]*domain.OrderItem)}
}

// addItem mirrors the insert order_items' real counterpart performs as part
// of OrderRepository.CreateFromPlan's transaction; only
// fakeOrderRepository.CreateFromPlan calls it.
func (f *fakeVendorOrderRepository) addItem(item *domain.OrderItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[item.VendorOrderID] = append(f.items[item.VendorOrderID], item)
}

func (f *fakeVendorOrderRepository) ListItemsByVendorOrderIDs(_ context.Context, vendorOrderIDs []string) (map[string][]*domain.OrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]*domain.OrderItem, len(vendorOrderIDs))
	for _, id := range vendorOrderIDs {
		if items, ok := f.items[id]; ok {
			out[id] = items
		}
	}
	return out, nil
}

// create mirrors the insert VendorOrderRepository.FindByID's real
// counterpart performs as part of OrderRepository.CreateFromPlan's
// transaction; only fakeOrderRepository.CreateFromPlan calls it.
func (f *fakeVendorOrderRepository) create(vo *domain.VendorOrder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[vo.ID] = vo
}

func (f *fakeVendorOrderRepository) FindByID(_ context.Context, id string) (*domain.VendorOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrVendorOrderNotFound
	}
	cp := *vo
	return &cp, nil
}

func (f *fakeVendorOrderRepository) ListByVendor(_ context.Context, vendorID string, _, _ int) ([]*domain.VendorOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.VendorOrder
	for _, vo := range f.byID {
		if vo.VendorID == vendorID {
			cp := *vo
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeVendorOrderRepository) ListByOrderID(_ context.Context, orderID string) ([]*domain.VendorOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.VendorOrder
	for _, vo := range f.byID {
		if vo.OrderID == orderID {
			cp := *vo
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeVendorOrderRepository) UpdateStatus(_ context.Context, id string, status domain.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorOrderNotFound
	}
	vo.Status = status
	return nil
}

func (f *fakeVendorOrderRepository) SetCommission(_ context.Context, id string, rateBps int, commissionAmount, netAmount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorOrderNotFound
	}
	vo.CommissionRateBps, vo.CommissionAmount, vo.NetAmount = &rateBps, &commissionAmount, &netAmount
	return nil
}

func (f *fakeVendorOrderRepository) SetShippingFee(_ context.Context, id string, feeAmount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.byID[id]
	if !ok {
		return repository.ErrVendorOrderNotFound
	}
	vo.ShippingFeeAmount = feeAmount
	return nil
}

func (f *fakeVendorOrderRepository) SummaryByVendor(_ context.Context, vendorID string) (*domain.VendorSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &domain.VendorSummary{}
	for _, vo := range f.byID {
		if vo.VendorID != vendorID || vo.Status == domain.StatusPendingPayment {
			continue
		}
		s.TotalOrders++
		s.TotalRevenue += vo.SubtotalAmount
		if vo.CommissionAmount != nil {
			s.TotalCommission += *vo.CommissionAmount
		}
		if vo.NetAmount != nil {
			s.TotalNet += *vo.NetAmount
		}
	}
	return s, nil
}

func (f *fakeVendorOrderRepository) TopProductsByVendor(_ context.Context, _ string, _ int) ([]*domain.TopProduct, error) {
	return nil, nil
}

func (f *fakeVendorOrderRepository) QuantitySoldByProductIDs(_ context.Context, _ []string) (map[string]int64, error) {
	return nil, nil
}

type fakeCommissionRuleRepository struct {
	mu     sync.Mutex
	rules  []*domain.CommissionRule
	nextID int
}

func newFakeCommissionRuleRepository(defaultRateBps int) *fakeCommissionRuleRepository {
	f := &fakeCommissionRuleRepository{}
	if defaultRateBps >= 0 {
		f.rules = append(f.rules, &domain.CommissionRule{ID: "rule-0", RateBps: defaultRateBps})
	}
	return f
}

func (f *fakeCommissionRuleRepository) Create(_ context.Context, rule *domain.CommissionRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	rule.ID = fmt.Sprintf("rule-%d", f.nextID)
	f.rules = append(f.rules, rule)
	return nil
}

func (f *fakeCommissionRuleRepository) FindCurrent(_ context.Context) (*domain.CommissionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.rules) == 0 {
		return nil, repository.ErrCommissionRuleNotFound
	}
	return f.rules[len(f.rules)-1], nil
}

func (f *fakeCommissionRuleRepository) List(_ context.Context, _, _ int) ([]*domain.CommissionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rules, nil
}

// fakeCartGateway simulates Cart's internal checkout contract: byBuyer holds
// each buyer's cart lines; Snapshot freezes them per operation (replaying
// the same operation id), Consume records what was purchased.
type fakeCartGateway struct {
	mu          sync.Mutex
	byBuyer     map[string][]adapter.CartLine
	snapshots   map[string]*adapter.CartSnapshot
	consumed    map[string][]adapter.CartConsumeLine // by buyer
	consumeErr  error
	snapshotErr error
	calls       int
}

func newFakeCartGateway() *fakeCartGateway {
	return &fakeCartGateway{byBuyer: map[string][]adapter.CartLine{}, snapshots: map[string]*adapter.CartSnapshot{}, consumed: map[string][]adapter.CartConsumeLine{}}
}

func (f *fakeCartGateway) Snapshot(_ context.Context, buyerID, operationID string, _ *int64) (*adapter.CartSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshotErr != nil {
		return nil, f.snapshotErr
	}
	if snap, ok := f.snapshots[operationID]; ok {
		return snap, nil
	}
	snap := &adapter.CartSnapshot{OperationID: operationID, CartVersion: 1}
	for i, line := range f.byBuyer[buyerID] {
		if line.LineID == "" {
			line.LineID = fmt.Sprintf("line-%d", i)
		}
		snap.Lines = append(snap.Lines, line)
	}
	f.snapshots[operationID] = snap
	return snap, nil
}

func (f *fakeCartGateway) Consume(_ context.Context, buyerID, _ string, lines []adapter.CartConsumeLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.consumeErr != nil {
		return f.consumeErr
	}
	f.consumed[buyerID] = append(f.consumed[buyerID], lines...)
	return nil
}

// fakeCartConsumptionRepository stores consume tasks; CreateFromPlan writes
// into it like the real repository does in the order transaction.
type fakeCartConsumptionRepository struct {
	mu    sync.Mutex
	tasks map[string]*domain.CartConsumption
}

func newFakeCartConsumptionRepository() *fakeCartConsumptionRepository {
	return &fakeCartConsumptionRepository{tasks: map[string]*domain.CartConsumption{}}
}

func (f *fakeCartConsumptionRepository) insert(orderID string, c *domain.CartConsumption) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyTask := *c
	copyTask.OrderID, copyTask.Status, copyTask.CreatedAt = orderID, domain.CartConsumptionHeld, time.Now()
	f.tasks[orderID] = &copyTask
}

func (f *fakeCartConsumptionRepository) get(orderID string) *domain.CartConsumption {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[orderID]; ok {
		copyTask := *t
		return &copyTask
	}
	return nil
}

func (f *fakeCartConsumptionRepository) setStatus(orderID string, from []domain.CartConsumptionStatus, to domain.CartConsumptionStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[orderID]; ok {
		for _, s := range from {
			if t.Status == s {
				t.Status = to
				return
			}
		}
	}
}

func (f *fakeCartConsumptionRepository) ListOpenByBuyer(_ context.Context, buyerID string) ([]*domain.CartConsumption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.CartConsumption
	for _, t := range f.tasks {
		if t.BuyerID == buyerID && (t.Status == domain.CartConsumptionHeld || t.Status == domain.CartConsumptionPending) {
			copyTask := *t
			out = append(out, &copyTask)
		}
	}
	return out, nil
}

func (f *fakeCartConsumptionRepository) Activate(_ context.Context, orderID string) error {
	f.setStatus(orderID, []domain.CartConsumptionStatus{domain.CartConsumptionHeld}, domain.CartConsumptionPending)
	return nil
}

func (f *fakeCartConsumptionRepository) Cancel(_ context.Context, orderID string) error {
	f.setStatus(orderID, []domain.CartConsumptionStatus{domain.CartConsumptionHeld}, domain.CartConsumptionCancelled)
	return nil
}

func (f *fakeCartConsumptionRepository) MarkConsumed(_ context.Context, orderID string) error {
	f.setStatus(orderID, []domain.CartConsumptionStatus{domain.CartConsumptionPending, domain.CartConsumptionParked}, domain.CartConsumptionConsumed)
	return nil
}

func (f *fakeCartConsumptionRepository) RecordFailure(_ context.Context, orderID, reason string, park bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[orderID]
	if !ok || t.Status != domain.CartConsumptionPending {
		return nil
	}
	t.Attempts++
	t.LastError = &reason
	if park || t.Attempts >= domain.MaxCartConsumeAttempts {
		t.Status = domain.CartConsumptionParked
	}
	return nil
}

func (f *fakeCartConsumptionRepository) ClaimDue(_ context.Context, limit int) ([]*domain.CartConsumption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.CartConsumption
	for _, t := range f.tasks {
		if t.Status == domain.CartConsumptionPending && len(out) < limit {
			copyTask := *t
			out = append(out, &copyTask)
		}
	}
	return out, nil
}

func (f *fakeCartConsumptionRepository) ListStaleHeld(_ context.Context, olderThan time.Time, limit int) ([]*domain.CartConsumption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.CartConsumption
	for _, t := range f.tasks {
		if t.Status == domain.CartConsumptionHeld && t.CreatedAt.Before(olderThan) && len(out) < limit {
			copyTask := *t
			out = append(out, &copyTask)
		}
	}
	return out, nil
}

func (f *fakeCartConsumptionRepository) Stats(context.Context) (domain.CartConsumptionStats, error) {
	return domain.CartConsumptionStats{}, nil
}

type fakeCatalogGateway struct {
	products   map[string]*adapter.ProductInfo
	variants   map[string]*adapter.VariantInfo
	getProduct func(string) (*adapter.ProductInfo, error)
}

func newFakeCatalogGateway() *fakeCatalogGateway {
	return &fakeCatalogGateway{products: make(map[string]*adapter.ProductInfo), variants: make(map[string]*adapter.VariantInfo)}
}

func (f *fakeCatalogGateway) GetProduct(_ context.Context, productID string) (*adapter.ProductInfo, error) {
	if f.getProduct != nil {
		return f.getProduct(productID)
	}
	p, ok := f.products[productID]
	if !ok {
		return nil, apperror.NotFound("Product not found")
	}
	return p, nil
}

func (f *fakeCatalogGateway) GetVariant(_ context.Context, variantID string) (*adapter.VariantInfo, error) {
	v, ok := f.variants[variantID]
	if !ok {
		return nil, apperror.NotFound("Product option not found")
	}
	return v, nil
}

type fakeVendorGateway struct {
	saleErr         error
	approvedVendors map[string]string
}

func newFakeVendorGateway() *fakeVendorGateway {
	return &fakeVendorGateway{approvedVendors: make(map[string]string)}
}

func (f *fakeVendorGateway) GetApprovedVendorID(_ context.Context, userID, vendorID string) (string, error) {
	approved, ok := f.approvedVendors[userID]
	if !ok || approved != vendorID {
		return "", apperror.Forbidden("You must have an approved vendor account")
	}
	return vendorID, nil
}

// fakeInventoryGateway simulates Inventory: shortProduct forces a
// reservation failure for a specific product id, so checkout's
// compensating cancel path can be exercised without a real service.
type fakeInventoryGateway struct {
	commitError     error
	mu              sync.Mutex
	shortProduct    string
	reservedOrders  map[string][]adapter.ReserveLine
	releasedOrders  map[string]bool
	committedOrders map[string]bool
}

func newFakeInventoryGateway() *fakeInventoryGateway {
	return &fakeInventoryGateway{
		reservedOrders:  make(map[string][]adapter.ReserveLine),
		releasedOrders:  make(map[string]bool),
		committedOrders: make(map[string]bool),
	}
}

func (f *fakeInventoryGateway) Commit(_ context.Context, orderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commitError != nil {
		return f.commitError
	}
	f.committedOrders[orderID] = true
	return nil
}

type sentNotification struct {
	userID      string
	notifType   string
	referenceID string
}

type fakeNotificationGateway struct {
	mu   sync.Mutex
	sent []sentNotification
}

func newFakeNotificationGateway() *fakeNotificationGateway {
	return &fakeNotificationGateway{}
}

func (f *fakeNotificationGateway) Notify(_ context.Context, userID, notifType, referenceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNotification{userID: userID, notifType: notifType, referenceID: referenceID})
	return nil
}

func (f *fakeInventoryGateway) Reserve(_ context.Context, orderID string, lines []adapter.ReserveLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range lines {
		if line.ProductID == f.shortProduct {
			return apperror.Conflict("Not enough stock available for product " + line.ProductID)
		}
	}
	f.reservedOrders[orderID] = lines
	return nil
}

func (f *fakeInventoryGateway) Release(_ context.Context, orderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releasedOrders[orderID] = true
	return nil
}

type fakeBuyerAddressRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.BuyerAddress
	nextID int
}

func newFakeBuyerAddressRepository() *fakeBuyerAddressRepository {
	return &fakeBuyerAddressRepository{byID: map[string]*domain.BuyerAddress{}}
}

func (f *fakeBuyerAddressRepository) Create(_ context.Context, a *domain.BuyerAddress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	a.ID = fmt.Sprintf("address-%d", f.nextID)
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeBuyerAddressRepository) FindByID(_ context.Context, id string) (*domain.BuyerAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrBuyerAddressNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeBuyerAddressRepository) ListForBuyer(_ context.Context, buyerID string) ([]*domain.BuyerAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.BuyerAddress
	for _, a := range f.byID {
		if a.BuyerID == buyerID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeBuyerAddressRepository) FindDefaultForBuyer(_ context.Context, buyerID string) (*domain.BuyerAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.byID {
		if a.BuyerID == buyerID && a.IsDefault {
			cp := *a
			return &cp, nil
		}
	}
	return nil, repository.ErrBuyerAddressNotFound
}

func (f *fakeBuyerAddressRepository) Update(_ context.Context, id string, a *domain.BuyerAddress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.byID[id]
	if !ok {
		return repository.ErrBuyerAddressNotFound
	}
	existing.RecipientName, existing.Phone = a.RecipientName, a.Phone
	existing.Province, existing.District, existing.Ward, existing.StreetAddress = a.Province, a.District, a.Ward, a.StreetAddress
	return nil
}

func (f *fakeBuyerAddressRepository) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[id]; !ok {
		return repository.ErrBuyerAddressNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeBuyerAddressRepository) SetDefault(_ context.Context, buyerID, addressID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, a := range f.byID {
		if a.ID == addressID && a.BuyerID == buyerID {
			found = true
		}
	}
	if !found {
		return repository.ErrBuyerAddressNotFound
	}
	for _, a := range f.byID {
		if a.BuyerID == buyerID {
			a.IsDefault = a.ID == addressID
		}
	}
	return nil
}

// fakeShipmentGateway simulates Shipment: every CreateShipment call
// succeeds with a fixed fee unless failVendorOrder matches, letting tests
// exercise the best-effort failure path without a real service.
type fakeShipmentGateway struct {
	mu        sync.Mutex
	feeAmount int64
	failAll   bool
	created   map[string]adapter.CreateShipmentInput
	cancelled map[string]bool
}

func newFakeShipmentGateway(feeAmount int64) *fakeShipmentGateway {
	return &fakeShipmentGateway{feeAmount: feeAmount, created: map[string]adapter.CreateShipmentInput{}, cancelled: map[string]bool{}}
}

func (f *fakeShipmentGateway) CreateShipment(_ context.Context, in adapter.CreateShipmentInput) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll {
		return "", 0, apperror.Internal(fmt.Errorf("shipment service unreachable"))
	}
	f.created[in.VendorOrderID] = in
	return "shipment-" + in.VendorOrderID, f.feeAmount, nil
}

func (f *fakeShipmentGateway) CancelForVendorOrder(_ context.Context, vendorOrderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled[vendorOrderID] = true
	return nil
}

func (f *fakeVendorGateway) Approved(ctx context.Context, ids []string) (map[string]int64, error) {
	if f.saleErr != nil {
		return nil, f.saleErr
	}
	out := map[string]int64{}
	for _, id := range ids {
		out[id] = 1
	}
	return out, nil
}

func (f *fakeOrderRepository) WithLockedOrder(ctx context.Context, id string, fn func(context.Context) error) error {
	return fn(ctx)
}
func (f *fakeInventoryGateway) Operation(ctx context.Context, id string) (*adapter.ReservationReceipt, error) {
	return &adapter.ReservationReceipt{OrderID: id, OperationID: id, Status: "held", ExpiresAt: time.Now().Add(30 * time.Minute)}, nil
}
