package usecase_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

// fakeStore is an in-memory cart database. txMu plays the role of the cart
// row lock: a fake transaction holds it for its whole duration, so
// mutations and consume are serialized like with FOR UPDATE. There is no
// rollback — rollback behavior is covered by the PostgreSQL integration
// tests.
type fakeStore struct {
	txMu    sync.Mutex
	mu      sync.Mutex
	seq     int
	carts   map[string]*domain.Cart // by user id
	items   map[string]*domain.CartItem
	ops     map[string]*domain.CheckoutOperation
	failAll error
}

func newFakeStore() *fakeStore {
	return &fakeStore{carts: map[string]*domain.Cart{}, items: map[string]*domain.CartItem{}, ops: map[string]*domain.CheckoutOperation{}}
}

func (s *fakeStore) nextID(prefix string) string {
	s.seq++
	return prefix + "-" + strconv.Itoa(s.seq)
}

type fakeTx struct{ store *fakeStore }

func (t fakeTx) Run(ctx context.Context, fn func(context.Context) error) error {
	t.store.txMu.Lock()
	defer t.store.txMu.Unlock()
	return fn(ctx)
}

type fakeCartRepository struct{ store *fakeStore }

func (r fakeCartRepository) GetOrCreateForUser(_ context.Context, userID string) (*domain.Cart, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAll != nil {
		return nil, s.failAll
	}
	c, ok := s.carts[userID]
	if !ok {
		c = &domain.Cart{ID: "cart-" + userID, UserID: userID, Version: 1}
		s.carts[userID] = c
	}
	copyCart := *c
	return &copyCart, nil
}

func (r fakeCartRepository) LockForUser(ctx context.Context, userID string) (*domain.Cart, error) {
	return r.GetOrCreateForUser(ctx, userID)
}

func (r fakeCartRepository) BumpVersion(_ context.Context, cartID string) (int64, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.carts {
		if c.ID == cartID {
			c.Version++
			return c.Version, nil
		}
	}
	return 0, errors.New("cart not found")
}

type fakeCartItemRepository struct{ store *fakeStore }

func (r fakeCartItemRepository) sorted(filter func(*domain.CartItem) bool) []*domain.CartItem {
	var out []*domain.CartItem
	for _, item := range r.store.items {
		if filter(item) {
			copyItem := *item
			out = append(out, &copyItem)
		}
	}
	slices.SortFunc(out, func(a, b *domain.CartItem) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out
}

func (r fakeCartItemRepository) ListByCart(_ context.Context, cartID string) ([]*domain.CartItem, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.sorted(func(i *domain.CartItem) bool { return i.CartID == cartID }), nil
}

func (r fakeCartItemRepository) ListByIDs(_ context.Context, cartID string, ids []string) ([]*domain.CartItem, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.sorted(func(i *domain.CartItem) bool { return i.CartID == cartID && slices.Contains(ids, i.ID) }), nil
}

func (r fakeCartItemRepository) FindLine(_ context.Context, cartID, productID string, variantID *string) (*domain.CartItem, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, item := range r.store.items {
		if item.CartID == cartID && item.SameLine(productID, variantID) {
			copyItem := *item
			return &copyItem, nil
		}
	}
	return nil, nil
}

func (r fakeCartItemRepository) CountLines(_ context.Context, cartID string) (int, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	n := 0
	for _, item := range r.store.items {
		if item.CartID == cartID {
			n++
		}
	}
	return n, nil
}

func (r fakeCartItemRepository) HasOtherCurrency(_ context.Context, cartID, currency string, exceptLineID *string) (bool, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, item := range r.store.items {
		if item.CartID != cartID || item.SeenCurrency == nil || *item.SeenCurrency == currency {
			continue
		}
		if exceptLineID != nil && item.ID == *exceptLineID {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r fakeCartItemRepository) Insert(_ context.Context, item *domain.CartItem) error {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.items {
		if existing.CartID == item.CartID && existing.SameLine(item.ProductID, item.VariantID) {
			return errors.New("unique violation")
		}
	}
	item.ID = s.nextID("line")
	item.Version = 1
	item.CreatedAt = time.Now().Add(time.Duration(s.seq) * time.Microsecond)
	copyItem := *item
	s.items[item.ID] = &copyItem
	return nil
}

func (r fakeCartItemRepository) Update(_ context.Context, item *domain.CartItem) error {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.items[item.ID]
	if !ok {
		return errors.New("line not found")
	}
	stored.Quantity, stored.SeenPriceAmount, stored.SeenCurrency = item.Quantity, item.SeenPriceAmount, item.SeenCurrency
	stored.Version++
	item.Version = stored.Version
	return nil
}

func (r fakeCartItemRepository) Delete(_ context.Context, cartID, lineID string) (bool, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[lineID]
	if !ok || item.CartID != cartID {
		return false, nil
	}
	delete(s.items, lineID)
	return true, nil
}

func (r fakeCartItemRepository) DeleteAll(_ context.Context, cartID string) (int64, error) {
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, item := range s.items {
		if item.CartID == cartID {
			delete(s.items, id)
			n++
		}
	}
	return n, nil
}

type fakeOperationRepository struct{ store *fakeStore }

func (r fakeOperationRepository) Find(_ context.Context, operationID string) (*domain.CheckoutOperation, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	op, ok := r.store.ops[operationID]
	if !ok {
		return nil, nil
	}
	copyOp := *op
	return &copyOp, nil
}

func (r fakeOperationRepository) Insert(_ context.Context, op *domain.CheckoutOperation) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, exists := r.store.ops[op.OperationID]; exists {
		return errors.New("duplicate operation")
	}
	op.CreatedAt = time.Now()
	copyOp := *op
	r.store.ops[op.OperationID] = &copyOp
	return nil
}

func (r fakeOperationRepository) MarkConsumed(_ context.Context, operationID, hash string, receipt *domain.ConsumeReceipt) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	op, ok := r.store.ops[operationID]
	if !ok || op.Receipt != nil {
		return errors.New("cannot mark consumed")
	}
	op.ConsumeHash, op.Receipt = &hash, receipt
	return nil
}

type fakeCatalogGateway struct {
	mu       sync.Mutex
	products map[string]*adapter.ProductInfo
	variants map[string]*adapter.VariantInfo
	failing  map[string]bool
	calls    int
}

func newFakeCatalogGateway() *fakeCatalogGateway {
	return &fakeCatalogGateway{products: map[string]*adapter.ProductInfo{}, variants: map[string]*adapter.VariantInfo{}, failing: map[string]bool{}}
}

func (f *fakeCatalogGateway) GetProduct(_ context.Context, productID string) (*adapter.ProductInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failing[productID] {
		return nil, apperror.Internal(errors.New("catalog timeout"))
	}
	p, ok := f.products[productID]
	if !ok {
		return nil, apperror.NotFound("Product not found")
	}
	copyProduct := *p
	return &copyProduct, nil
}

func (f *fakeCatalogGateway) GetVariant(_ context.Context, variantID string) (*adapter.VariantInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failing[variantID] {
		return nil, apperror.Internal(errors.New("catalog timeout"))
	}
	v, ok := f.variants[variantID]
	if !ok {
		return nil, apperror.NotFound("Product option not found")
	}
	return v, nil
}

type fakeInventoryGateway struct {
	mu           sync.Mutex
	variantStock map[string]int64
	productStock map[string]int64
	down         bool
}

func newFakeInventoryGateway() *fakeInventoryGateway {
	return &fakeInventoryGateway{variantStock: map[string]int64{}, productStock: map[string]int64{}}
}

func (f *fakeInventoryGateway) GetVariantStock(_ context.Context, variantIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("inventory unavailable")
	}
	out := map[string]int64{}
	for _, id := range variantIDs {
		if q, ok := f.variantStock[id]; ok {
			out[id] = q
		}
	}
	return out, nil
}

func (f *fakeInventoryGateway) GetProductStock(_ context.Context, productID string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return 0, false, errors.New("inventory unavailable")
	}
	q, ok := f.productStock[productID]
	return q, ok, nil
}

type fakeRetentionRepository struct {
	cartCalls, opCalls []time.Time
	cartBatches        []int64
	opBatches          []int64
}

func (f *fakeRetentionRepository) PurgeIdleCarts(_ context.Context, idleBefore time.Time, _ int) (int64, error) {
	f.cartCalls = append(f.cartCalls, idleBefore)
	if len(f.cartBatches) == 0 {
		return 0, nil
	}
	n := f.cartBatches[0]
	f.cartBatches = f.cartBatches[1:]
	return n, nil
}

func (f *fakeRetentionRepository) PurgeOperations(_ context.Context, createdBefore time.Time, _ int) (int64, error) {
	f.opCalls = append(f.opCalls, createdBefore)
	if len(f.opBatches) == 0 {
		return 0, nil
	}
	n := f.opBatches[0]
	f.opBatches = f.opBatches[1:]
	return n, nil
}
