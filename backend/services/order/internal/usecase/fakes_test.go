package usecase_test

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// In-memory fakes of Order's repositories and gateways. Transactions have
// no rollback here; atomicity is covered by the PostgreSQL integration
// tests. WithLockedOrder serializes like the order row lock.

type fakeOrderRepository struct {
	mu           sync.Mutex
	txMu         sync.Mutex
	byID         map[string]*domain.Order
	items        map[string][]*domain.OrderItem
	nextID       int
	vendorOrders *fakeVendorOrderRepository
	consumptions *fakeCartConsumptionRepository
	checkoutOps  *fakeCheckoutOpRepository
	createErr    error
}

func newFakeOrderRepository(vendorOrders *fakeVendorOrderRepository) *fakeOrderRepository {
	return &fakeOrderRepository{byID: map[string]*domain.Order{}, items: map[string][]*domain.OrderItem{}, vendorOrders: vendorOrders}
}

func (f *fakeOrderRepository) WithLockedOrder(ctx context.Context, id string, fn func(context.Context) error) error {
	f.txMu.Lock()
	defer f.txMu.Unlock()
	f.mu.Lock()
	_, ok := f.byID[id]
	f.mu.Unlock()
	if !ok {
		return apperror.NotFound("Order not found")
	}
	return fn(ctx)
}

func (f *fakeOrderRepository) CreateFromPlan(_ context.Context, plan *domain.Plan) (*domain.Order, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.mu.Lock()
	f.nextID++
	order := plan.Order
	order.ID = fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID)
	order.Version, order.CreatedAt, order.UpdatedAt = 1, time.Now(), time.Now()
	if order.CheckoutState == "" {
		order.CheckoutState = domain.CheckoutReady
	}
	stored := order
	f.byID[order.ID] = &stored
	vendorOrderIDs := make([]string, len(plan.VendorOrders))
	for i, vo := range plan.VendorOrders {
		vendorOrderIDs[i] = "vo-" + strconv.Itoa(f.nextID) + "-" + strconv.Itoa(i)
		cp := vo
		cp.ID, cp.OrderID, cp.Version, cp.CreatedAt = vendorOrderIDs[i], order.ID, 1, time.Now()
		if cp.Commission != nil {
			cp.CommissionRateBps, cp.CommissionAmount, cp.NetAmount = &cp.Commission.RateBps, &cp.Commission.Amount, &cp.Commission.NetAmount
		}
		f.vendorOrders.create(&cp)
	}
	for i, item := range plan.Items {
		idx, _ := strconv.Atoi(item.VendorOrderID)
		cp := item
		cp.ID, cp.OrderID, cp.VendorOrderID = fmt.Sprintf("item-%d-%d", f.nextID, i), order.ID, vendorOrderIDs[idx]
		f.items[order.ID] = append(f.items[order.ID], &cp)
		f.vendorOrders.addItem(&cp)
	}
	f.mu.Unlock()
	if plan.CartConsumption != nil && f.consumptions != nil {
		f.consumptions.insert(order.ID, plan.CartConsumption)
	}
	if plan.CheckoutOperationID != "" && f.checkoutOps != nil {
		f.checkoutOps.link(plan.CheckoutOperationID, order.ID)
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
	cp := *o
	return &cp, nil
}

func (f *fakeOrderRepository) get(id string) *domain.Order {
	o, _ := f.FindByID(context.Background(), id)
	return o
}

func (f *fakeOrderRepository) List(_ context.Context, filter repository.OrderFilter, limit, offset int) ([]*domain.Order, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Order
	for _, o := range f.byID {
		if (filter.BuyerID == "" || o.BuyerID == filter.BuyerID) && (filter.Status == "" || string(o.Status) == filter.Status) {
			cp := *o
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	total := int64(len(out))
	if offset > len(out) {
		offset = len(out)
	}
	end := min(offset+limit, len(out))
	return out[offset:end], total, nil
}

func (f *fakeOrderRepository) TransitionStatus(_ context.Context, id string, from, to domain.Status, reason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok || o.Status != from {
		return repository.ErrStaleState
	}
	o.Status = to
	o.Version++
	if reason != nil {
		o.CancellationReason = reason
	}
	if to == domain.StatusPaid {
		now := time.Now()
		o.PaidAt = &now
	}
	return nil
}

func (f *fakeOrderRepository) SetCheckoutState(_ context.Context, id string, state domain.CheckoutState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.byID[id]
	if !ok || o.CheckoutState != domain.CheckoutPreparing {
		return repository.ErrStaleState
	}
	o.CheckoutState = state
	return nil
}

func (f *fakeOrderRepository) AddRefunded(_ context.Context, id string, amount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id].RefundedAmount += amount
	return nil
}

func (f *fakeOrderRepository) ListItemsByOrder(_ context.Context, orderID string) ([]*domain.OrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[orderID], nil
}

func (f *fakeOrderRepository) FindItem(_ context.Context, orderID, itemID string) (*domain.OrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items[orderID] {
		if item.ID == itemID {
			cp := *item
			return &cp, nil
		}
	}
	return nil, repository.ErrOrderNotFound
}

func (f *fakeOrderRepository) ListReviewEligibility(context.Context, string, string) ([]*domain.ReviewEligibility, error) {
	return nil, nil
}

type fakeVendorOrderRepository struct {
	mu    sync.Mutex
	byID  map[string]*domain.VendorOrder
	items map[string][]*domain.OrderItem
}

func newFakeVendorOrderRepository() *fakeVendorOrderRepository {
	return &fakeVendorOrderRepository{byID: map[string]*domain.VendorOrder{}, items: map[string][]*domain.OrderItem{}}
}

func (f *fakeVendorOrderRepository) create(vo *domain.VendorOrder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[vo.ID] = vo
}

func (f *fakeVendorOrderRepository) addItem(item *domain.OrderItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[item.VendorOrderID] = append(f.items[item.VendorOrderID], item)
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

func (f *fakeVendorOrderRepository) ListByVendor(_ context.Context, vendorID, status string, _, _ int) ([]*domain.VendorOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.VendorOrder
	for _, vo := range f.byID {
		if vo.VendorID == vendorID && (status == "" || string(vo.Status) == status) {
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeVendorOrderRepository) TransitionStatus(_ context.Context, id string, from, to domain.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.byID[id]
	if !ok || vo.Status != from {
		return repository.ErrStaleState
	}
	vo.Status = to
	if to == domain.StatusCompleted {
		now := time.Now()
		vo.CompletedAt = &now
	}
	return nil
}

func (f *fakeVendorOrderRepository) TransitionAllForOrder(_ context.Context, orderID string, from, to domain.Status) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, vo := range f.byID {
		if vo.OrderID == orderID && vo.Status == from {
			vo.Status = to
			n++
		}
	}
	return n, nil
}

func (f *fakeVendorOrderRepository) SetLegacyCommission(_ context.Context, id string, rule *domain.CommissionRule, amount, net, base int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo := f.byID[id]
	if vo.CommissionRateBps != nil {
		return nil
	}
	rate := rule.RateBps
	vo.CommissionRateBps, vo.CommissionAmount, vo.NetAmount = &rate, &amount, &net
	vo.Commission = &domain.CommissionSnapshot{RuleID: &rule.ID, RuleVersion: &rule.Version, RateBps: rate, BaseAmount: base, Amount: amount, NetAmount: net, Source: domain.CommissionSourceLegacy}
	return nil
}

func (f *fakeVendorOrderRepository) AddRefunded(_ context.Context, id string, amount int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[id].RefundedAmount += amount
	return f.byID[id].RefundedAmount, nil
}

func (f *fakeVendorOrderRepository) HeldForSettlement(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (f *fakeVendorOrderRepository) SummaryByVendor(_ context.Context, vendorID string) (*domain.VendorSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &domain.VendorSummary{}
	for _, vo := range f.byID {
		if vo.VendorID != vendorID || !vo.Status.PaidOrFurther() {
			continue
		}
		s.TotalOrders++
		s.TotalRevenue += vo.SubtotalAmount
		s.TotalRefunded += vo.RefundedAmount
	}
	return s, nil
}

func (f *fakeVendorOrderRepository) TopProductsByVendor(context.Context, string, int) ([]*domain.TopProduct, error) {
	return nil, nil
}

func (f *fakeVendorOrderRepository) QuantitySoldByProductIDs(context.Context, []string) (map[string]int64, error) {
	return nil, nil
}

func (f *fakeVendorOrderRepository) ListItemsByVendorOrderIDs(_ context.Context, ids []string) (map[string][]*domain.OrderItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]*domain.OrderItem{}
	for _, id := range ids {
		if items, ok := f.items[id]; ok {
			out[id] = items
		}
	}
	return out, nil
}

type fakeCommissionRuleRepository struct {
	mu    sync.Mutex
	rules []*domain.CommissionRule
}

func newFakeCommissionRuleRepository(defaultRateBps int) *fakeCommissionRuleRepository {
	f := &fakeCommissionRuleRepository{}
	if defaultRateBps >= 0 {
		f.rules = append(f.rules, &domain.CommissionRule{ID: "rule-1", Version: 1, RateBps: defaultRateBps})
	}
	return f
}

func (f *fakeCommissionRuleRepository) Create(_ context.Context, rule *domain.CommissionRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rule.Version = int64(len(f.rules) + 1)
	rule.ID = fmt.Sprintf("rule-%d", rule.Version)
	f.rules = append(f.rules, rule)
	return nil
}

func (f *fakeCommissionRuleRepository) FindCurrent(context.Context) (*domain.CommissionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.rules) == 0 {
		return nil, repository.ErrCommissionRuleNotFound
	}
	return f.rules[len(f.rules)-1], nil
}

func (f *fakeCommissionRuleRepository) List(context.Context, int, int) ([]*domain.CommissionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rules, nil
}

type fakeCheckoutOpRepository struct {
	mu  sync.Mutex
	ops map[string]*domain.CheckoutOperation // by id
	seq int
}

func newFakeCheckoutOpRepository() *fakeCheckoutOpRepository {
	return &fakeCheckoutOpRepository{ops: map[string]*domain.CheckoutOperation{}}
}

func (f *fakeCheckoutOpRepository) link(id, orderID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops[id].OrderID = &orderID
}

func (f *fakeCheckoutOpRepository) Begin(_ context.Context, buyerID, key, hash string, ttl time.Duration) (*domain.CheckoutOperation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, op := range f.ops {
		if op.BuyerID == buyerID && op.IdempotencyKey == key {
			restart := op.RequestHash == hash && op.Status == domain.CheckoutOpFailed && !domain.ReplayableFailure(op.StoredError())
			if !restart {
				cp := *op
				return &cp, false, nil
			}
			op.Status, op.OrderID, op.ErrorCode, op.ErrorMessage, op.ErrorStatus = domain.CheckoutOpPreparing, nil, nil, nil, nil
			cp := *op
			return &cp, true, nil
		}
	}
	f.seq++
	op := &domain.CheckoutOperation{ID: fmt.Sprintf("op-%d", f.seq), BuyerID: buyerID, IdempotencyKey: key, RequestHash: hash,
		Status: domain.CheckoutOpPreparing, ExpiresAt: time.Now().Add(ttl), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.ops[op.ID] = op
	cp := *op
	return &cp, true, nil
}

func (f *fakeCheckoutOpRepository) Complete(_ context.Context, id, orderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := f.ops[id]
	if op.Status != domain.CheckoutOpPreparing {
		return repository.ErrStaleState
	}
	op.Status, op.OrderID = domain.CheckoutOpCompleted, &orderID
	return nil
}

func (f *fakeCheckoutOpRepository) Fail(_ context.Context, id string, cause *apperror.Error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := f.ops[id]
	if op.Status != domain.CheckoutOpPreparing {
		return nil
	}
	code, msg, status := string(cause.Code), cause.Message, cause.Status
	op.Status, op.ErrorCode, op.ErrorMessage, op.ErrorStatus = domain.CheckoutOpFailed, &code, &msg, &status
	return nil
}

func (f *fakeCheckoutOpRepository) FindByID(_ context.Context, id string) (*domain.CheckoutOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if op, ok := f.ops[id]; ok {
		cp := *op
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeCheckoutOpRepository) ListStalePreparing(_ context.Context, olderThan time.Time, limit int) ([]*domain.CheckoutOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.CheckoutOperation
	for _, op := range f.ops {
		if op.Status == domain.CheckoutOpPreparing && op.UpdatedAt.Before(olderThan) && len(out) < limit {
			cp := *op
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeCheckoutOpRepository) PurgeExpired(context.Context, int) (int64, error) { return 0, nil }

type fakePaymentRecordRepository struct {
	mu       sync.Mutex
	payments map[string]*domain.OrderPayment
}

func newFakePaymentRecordRepository() *fakePaymentRecordRepository {
	return &fakePaymentRecordRepository{payments: map[string]*domain.OrderPayment{}}
}

func (f *fakePaymentRecordRepository) Find(_ context.Context, id string) (*domain.OrderPayment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.payments[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}

func (f *fakePaymentRecordRepository) Insert(_ context.Context, p *domain.OrderPayment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.ReceivedAt = time.Now()
	cp := *p
	f.payments[p.PaymentID] = &cp
	return nil
}

func (f *fakePaymentRecordRepository) ListByOrder(_ context.Context, orderID string) ([]*domain.OrderPayment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.OrderPayment
	for _, p := range f.payments {
		if p.OrderID == orderID {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakePaymentRecordRepository) ListRejected(context.Context, int, int) ([]*domain.OrderPayment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.OrderPayment
	for _, p := range f.payments {
		if p.Outcome == domain.PaymentRejected {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}

type fakeEffectRepository struct {
	mu      sync.Mutex
	effects []*domain.Effect
	seq     int
}

func (f *fakeEffectRepository) Enqueue(_ context.Context, effects ...domain.Effect) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range effects {
		dup := false
		for _, existing := range f.effects {
			dup = dup || (existing.OrderID == e.OrderID && existing.Kind == e.Kind && existing.Target == e.Target)
		}
		if dup {
			continue
		}
		f.seq++
		cp := e
		cp.ID, cp.Status, cp.CreatedAt = fmt.Sprintf("effect-%d", f.seq), domain.EffectPending, time.Now()
		f.effects = append(f.effects, &cp)
	}
	return nil
}

func (f *fakeEffectRepository) ClaimDue(_ context.Context, orderID string, limit int) ([]*domain.Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Effect
	for _, e := range f.effects {
		if e.Status == domain.EffectPending && (orderID == "" || e.OrderID == orderID) && len(out) < limit {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeEffectRepository) find(id string) *domain.Effect {
	for _, e := range f.effects {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (f *fakeEffectRepository) MarkDone(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.find(id).Status = domain.EffectDone
	return nil
}

func (f *fakeEffectRepository) RecordFailure(_ context.Context, id, reason string, park bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.find(id)
	e.Attempts++
	e.LastError = &reason
	if park || e.Attempts >= domain.MaxEffectAttempts {
		e.Status = domain.EffectParked
		return true, nil
	}
	return false, nil
}

func (f *fakeEffectRepository) Replay(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.find(id)
	if e == nil || e.Status != domain.EffectParked {
		return false, nil
	}
	e.Status, e.Attempts = domain.EffectPending, 0
	return true, nil
}

func (f *fakeEffectRepository) ListByOrder(_ context.Context, orderID string) ([]*domain.Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Effect
	for _, e := range f.effects {
		if e.OrderID == orderID {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeEffectRepository) ListParked(context.Context, int, int) ([]*domain.Effect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Effect
	for _, e := range f.effects {
		if e.Status == domain.EffectParked {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeEffectRepository) Stats(context.Context) (domain.EffectStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s domain.EffectStats
	for _, e := range f.effects {
		switch e.Status {
		case domain.EffectPending:
			s.Pending++
		case domain.EffectParked:
			s.Parked++
		}
	}
	return s, nil
}

// count returns how many effects of a kind (and status, if given) exist.
func (f *fakeEffectRepository) count(kind domain.EffectKind, status string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.effects {
		if e.Kind == kind && (status == "" || e.Status == status) {
			n++
		}
	}
	return n
}

type fakeRefundRepository struct {
	mu      sync.Mutex
	refunds map[string]*domain.Refund
	seq     int
}

func newFakeRefundRepository() *fakeRefundRepository {
	return &fakeRefundRepository{refunds: map[string]*domain.Refund{}}
}

func (f *fakeRefundRepository) Create(_ context.Context, r *domain.Refund) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.refunds {
		if !existing.Status.Open() {
			continue
		}
		if (r.ReturnRequestID != nil && existing.ReturnRequestID != nil && *r.ReturnRequestID == *existing.ReturnRequestID) ||
			(r.PaymentID != nil && existing.PaymentID != nil && *r.PaymentID == *existing.PaymentID) {
			return apperror.Conflict("A refund is already open for this return or payment")
		}
	}
	f.seq++
	r.ID, r.Status, r.CreatedAt = fmt.Sprintf("00000000-0000-0000-0001-%012d", f.seq), domain.RefundRequested, time.Now()
	cp := *r
	f.refunds[r.ID] = &cp
	return nil
}

func (f *fakeRefundRepository) FindByID(_ context.Context, id string) (*domain.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.refunds[id]
	if !ok {
		return nil, repository.ErrRefundNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeRefundRepository) Transition(_ context.Context, id string, from, to domain.RefundStatus, paymentRefundID, failure *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.refunds[id]
	if r.Status != from {
		return repository.ErrStaleState
	}
	r.Status = to
	if paymentRefundID != nil {
		r.PaymentRefundID = paymentRefundID
	}
	if failure != nil {
		r.FailureReason = failure
	}
	return nil
}

func (f *fakeRefundRepository) OpenTotals(_ context.Context, orderID, vendorOrderID string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var order, vendor int64
	for _, r := range f.refunds {
		if r.OrderID != orderID || r.PaymentID != nil || !r.Status.Open() {
			continue
		}
		order += r.Amount
		if r.VendorOrderID != nil && *r.VendorOrderID == vendorOrderID {
			vendor += r.Amount
		}
	}
	return order, vendor, nil
}

func (f *fakeRefundRepository) ListByOrder(_ context.Context, orderID string) ([]*domain.Refund, error) {
	all, _ := f.List(context.Background(), "", 100, 0)
	var out []*domain.Refund
	for _, r := range all {
		if r.OrderID == orderID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRefundRepository) List(_ context.Context, status string, _, _ int) ([]*domain.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Refund
	for _, r := range f.refunds {
		if status == "" || string(r.Status) == status {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeRefundRepository) only() *domain.Refund {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.refunds {
		cp := *r
		return &cp
	}
	return nil
}

type fakeReturnRepository struct {
	mu        sync.Mutex
	returns   map[string]*domain.ReturnRequest
	events    []*domain.ReturnEvent
	seq       int
	vendorsOf func(itemID string) (string, string)
}

func newFakeReturnRepository() *fakeReturnRepository {
	return &fakeReturnRepository{returns: map[string]*domain.ReturnRequest{}}
}

func (f *fakeReturnRepository) Create(_ context.Context, rr *domain.ReturnRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.returns {
		if existing.OrderItemID == rr.OrderItemID && existing.Status != domain.ReturnRejected && existing.Status != domain.ReturnRefunded {
			return apperror.Conflict("This item already has an open return request")
		}
	}
	f.seq++
	rr.ID, rr.Status, rr.Version, rr.CreatedAt = fmt.Sprintf("00000000-0000-0000-0002-%012d", f.seq), domain.ReturnRequested, 1, time.Now()
	cp := *rr
	f.returns[rr.ID] = &cp
	return nil
}

func (f *fakeReturnRepository) ReturnedQuantity(_ context.Context, itemID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, rr := range f.returns {
		if rr.OrderItemID == itemID && rr.Status != domain.ReturnRejected {
			n += rr.Quantity
		}
	}
	return n, nil
}

func (f *fakeReturnRepository) FindByID(_ context.Context, id string) (*domain.ReturnRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rr, ok := f.returns[id]
	if !ok {
		return nil, repository.ErrReturnRequestNotFound
	}
	cp := *rr
	return &cp, nil
}

func (f *fakeReturnRepository) VendorOf(_ context.Context, id string) (string, string, error) {
	f.mu.Lock()
	rr, ok := f.returns[id]
	f.mu.Unlock()
	if !ok {
		return "", "", repository.ErrReturnRequestNotFound
	}
	vo, vendor := f.vendorsOf(rr.OrderItemID)
	return vo, vendor, nil
}

func (f *fakeReturnRepository) Transition(_ context.Context, rr *domain.ReturnRequest, to domain.ReturnRequestStatus, u repository.ReturnUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := f.returns[rr.ID]
	if stored.Status != rr.Status || stored.Version != rr.Version {
		return repository.ErrStaleState
	}
	stored.Status, stored.Version = to, stored.Version+1
	if u.Restock != nil {
		stored.Restock = u.Restock
	}
	if u.DecisionNote != nil {
		stored.DecisionNote = u.DecisionNote
	}
	rr.Status, rr.Version = to, stored.Version
	return nil
}

func (f *fakeReturnRepository) AddEvent(_ context.Context, e *domain.ReturnEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *e
	f.events = append(f.events, &cp)
	return nil
}

func (f *fakeReturnRepository) ListEvents(_ context.Context, returnID string) ([]*domain.ReturnEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.ReturnEvent
	for _, e := range f.events {
		if e.ReturnID == returnID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeReturnRepository) list(match func(*domain.ReturnRequest) bool) []*domain.ReturnRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.ReturnRequest
	for _, rr := range f.returns {
		if match(rr) {
			cp := *rr
			out = append(out, &cp)
		}
	}
	return out
}

func (f *fakeReturnRepository) ListByBuyer(_ context.Context, buyerID string, _, _ int) ([]*domain.ReturnRequest, error) {
	return f.list(func(r *domain.ReturnRequest) bool { return r.BuyerID == buyerID }), nil
}

func (f *fakeReturnRepository) ListByOrder(_ context.Context, orderID string) ([]*domain.ReturnRequest, error) {
	return f.list(func(r *domain.ReturnRequest) bool { return r.OrderID == orderID }), nil
}

func (f *fakeReturnRepository) ListByStatus(_ context.Context, status string, _, _ int) ([]*domain.ReturnRequest, error) {
	return f.list(func(r *domain.ReturnRequest) bool { return status == "" || string(r.Status) == status }), nil
}

func (f *fakeReturnRepository) ListForVendor(_ context.Context, vendorID, status string, _, _ int) ([]*domain.ReturnRequest, error) {
	return f.list(func(r *domain.ReturnRequest) bool {
		_, v := f.vendorsOf(r.OrderItemID)
		return v == vendorID && (status == "" || string(r.Status) == status)
	}), nil
}

// fakeCartGateway simulates Cart's internal contract.
type fakeCartGateway struct {
	mu          sync.Mutex
	byBuyer     map[string][]adapter.CartLine
	snapshots   map[string]*adapter.CartSnapshot
	consumed    map[string][]adapter.CartConsumeLine
	consumeErr  error
	snapshotErr error
	calls       int
}

func newFakeCartGateway() *fakeCartGateway {
	return &fakeCartGateway{byBuyer: map[string][]adapter.CartLine{}, snapshots: map[string]*adapter.CartSnapshot{}, consumed: map[string][]adapter.CartConsumeLine{}}
}

func (f *fakeCartGateway) lines(buyerID string) []adapter.CartLine {
	var out []adapter.CartLine
	for i, line := range f.byBuyer[buyerID] {
		if line.LineID == "" {
			line.LineID = fmt.Sprintf("line-%d", i)
		}
		out = append(out, line)
	}
	return out
}

func (f *fakeCartGateway) Lines(_ context.Context, buyerID string) (*adapter.CartSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &adapter.CartSnapshot{CartVersion: 1, Lines: f.lines(buyerID)}, nil
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
	snap := &adapter.CartSnapshot{OperationID: operationID, CartVersion: 1, Lines: f.lines(buyerID)}
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
	cp := *c
	cp.OrderID, cp.Status, cp.CreatedAt = orderID, domain.CartConsumptionHeld, time.Now()
	f.tasks[orderID] = &cp
}

func (f *fakeCartConsumptionRepository) get(orderID string) *domain.CartConsumption {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[orderID]; ok {
		cp := *t
		return &cp
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
			cp := *t
			out = append(out, &cp)
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
			cp := *t
			out = append(out, &cp)
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
			cp := *t
			out = append(out, &cp)
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
	return &fakeCatalogGateway{products: map[string]*adapter.ProductInfo{}, variants: map[string]*adapter.VariantInfo{}}
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
	return &fakeVendorGateway{approvedVendors: map[string]string{}}
}

func (f *fakeVendorGateway) GetApprovedVendorID(_ context.Context, userID, vendorID string) (string, error) {
	approved, ok := f.approvedVendors[userID]
	if !ok || (vendorID != "" && approved != vendorID) {
		return "", apperror.Forbidden("You must have an approved vendor account")
	}
	return approved, nil
}

func (f *fakeVendorGateway) Approved(_ context.Context, ids []string) (map[string]int64, error) {
	if f.saleErr != nil {
		return nil, f.saleErr
	}
	out := map[string]int64{}
	for _, id := range ids {
		out[id] = 1
	}
	return out, nil
}

// fakeInventoryGateway simulates Inventory. shortProduct forces a
// reservation failure; receiptStatus overrides Operation's answer.
type fakeInventoryGateway struct {
	mu              sync.Mutex
	commitError     error
	releaseError    error
	shortProduct    string
	receiptStatus   string
	reservedOrders  map[string][]adapter.ReserveLine
	releasedOrders  map[string]bool
	committedOrders map[string]bool
	restocked       map[string]int64
}

func newFakeInventoryGateway() *fakeInventoryGateway {
	return &fakeInventoryGateway{reservedOrders: map[string][]adapter.ReserveLine{}, releasedOrders: map[string]bool{},
		committedOrders: map[string]bool{}, restocked: map[string]int64{}}
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
	if f.releaseError != nil {
		return f.releaseError
	}
	f.releasedOrders[orderID] = true
	return nil
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

func (f *fakeInventoryGateway) Operation(_ context.Context, id string) (*adapter.ReservationReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.receiptStatus
	if status == "" {
		if _, ok := f.reservedOrders[id]; !ok {
			return nil, apperror.NotFound("Reservation operation not found")
		}
		status = "held"
	}
	return &adapter.ReservationReceipt{OrderID: id, OperationID: id, Status: status, ExpiresAt: time.Now().Add(30 * time.Minute)}, nil
}

func (f *fakeInventoryGateway) RestockReturn(_ context.Context, returnID, _ string, _ *string, quantity int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restocked[returnID] += quantity
	return nil
}

type sentNotification struct {
	userID, notifType, referenceID string
}

type fakeNotificationGateway struct {
	mu   sync.Mutex
	sent []sentNotification
}

func (f *fakeNotificationGateway) Notify(_ context.Context, userID, notifType, referenceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNotification{userID, notifType, referenceID})
	return nil
}

// fakeShipmentGateway quotes feeByVendor (default fee otherwise) and
// reports unavailable for vendors in unavailable.
type fakeShipmentGateway struct {
	mu          sync.Mutex
	fee         int64
	feeByVendor map[string]int64
	unavailable map[string]bool
	quoteErr    error
	createErr   error
	created     map[string]adapter.CreateShipmentInput
	cancelled   map[string]bool
}

func newFakeShipmentGateway(fee int64) *fakeShipmentGateway {
	return &fakeShipmentGateway{fee: fee, feeByVendor: map[string]int64{}, unavailable: map[string]bool{}, created: map[string]adapter.CreateShipmentInput{}, cancelled: map[string]bool{}}
}

func (f *fakeShipmentGateway) Quote(_ context.Context, vendorID, _ string, weight int64) (*domain.ShippingQuote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.quoteErr != nil {
		return nil, f.quoteErr
	}
	if f.unavailable[vendorID] {
		return nil, domain.ShippingUnavailable("Shipping is not available for this destination yet")
	}
	fee := f.fee
	if v, ok := f.feeByVendor[vendorID]; ok {
		fee = v
	}
	return &domain.ShippingQuote{VendorID: vendorID, FeeAmount: fee, Currency: "VND", CarrierID: "carrier-1", ZoneID: "zone-1",
		FeeRuleID: "fee-rule-" + vendorID, FeeRuleVersion: 1, PackageWeightGrams: weight, QuotedAt: time.Now()}, nil
}

func (f *fakeShipmentGateway) CreateShipment(_ context.Context, in adapter.CreateShipmentInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created[in.VendorOrderID] = in
	return "shipment-" + in.VendorOrderID, nil
}

func (f *fakeShipmentGateway) CancelForVendorOrder(_ context.Context, vendorOrderID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled[vendorOrderID] = true
	return nil
}

type fakePaymentGateway struct {
	mu          sync.Mutex
	err         error
	requests    []adapter.RefundRequest
	settlements []adapter.SettlementReport
}

func (f *fakePaymentGateway) SettleVendorOrder(_ context.Context, r adapter.SettlementReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.settlements = append(f.settlements, r)
	return nil
}

func (f *fakePaymentGateway) RequestRefund(_ context.Context, r adapter.RefundRequest) (*adapter.RefundReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.requests = append(f.requests, r)
	return &adapter.RefundReceipt{PaymentRefundID: "pr-" + r.RefundID, Status: "awaiting_provider_refund"}, nil
}

type fakeIdentityGateway struct {
	denied map[string]bool
}

func (f fakeIdentityGateway) RequireRole(_ context.Context, userID, _ string) error {
	if f.denied[userID] {
		return apperror.Forbidden("Role no longer granted")
	}
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
	a.ID = fmt.Sprintf("00000000-0000-0000-0003-%012d", f.nextID)
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
		found = found || (a.ID == addressID && a.BuyerID == buyerID)
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
