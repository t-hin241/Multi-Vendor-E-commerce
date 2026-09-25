package usecase_test

import (
	"context"
	"sync"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

type fakeCartRepository struct {
	mu   sync.Mutex
	byID map[string]*domain.Cart
}

func newFakeCartRepository() *fakeCartRepository {
	return &fakeCartRepository{byID: make(map[string]*domain.Cart)}
}

func (f *fakeCartRepository) GetOrCreateForUser(_ context.Context, userID string) (*domain.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.byID[userID]; ok {
		return c, nil
	}
	c := &domain.Cart{ID: "cart-" + userID, UserID: userID}
	f.byID[userID] = c
	return c, nil
}

// fakeCartItemRepository is keyed by cartID -> lineKey -> item, where
// lineKey is "product:<id>" for a product-level line or "variant:<id>" for
// a variant-scoped one — mirroring the real schema's two partial unique
// indexes (one product-level line and any number of variant-scoped lines
// can coexist for the same product, never two of the same kind).
type fakeCartItemRepository struct {
	mu     sync.Mutex
	byCart map[string]map[string]*domain.CartItem
}

func newFakeCartItemRepository() *fakeCartItemRepository {
	return &fakeCartItemRepository{byCart: make(map[string]map[string]*domain.CartItem)}
}

func productLineKey(productID string) string { return "product:" + productID }
func variantLineKey(variantID string) string { return "variant:" + variantID }

func (f *fakeCartItemRepository) AddQuantity(_ context.Context, cartID, productID string, delta int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCart(cartID)
	key := productLineKey(productID)
	if existing, ok := f.byCart[cartID][key]; ok {
		existing.Quantity += delta
		return nil
	}
	f.byCart[cartID][key] = &domain.CartItem{CartID: cartID, ProductID: productID, Quantity: delta}
	return nil
}

func (f *fakeCartItemRepository) AddQuantityForVariant(_ context.Context, cartID, productID, variantID string, delta int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCart(cartID)
	key := variantLineKey(variantID)
	if existing, ok := f.byCart[cartID][key]; ok {
		existing.Quantity += delta
		return nil
	}
	vid := variantID
	f.byCart[cartID][key] = &domain.CartItem{CartID: cartID, ProductID: productID, VariantID: &vid, Quantity: delta}
	return nil
}

func (f *fakeCartItemRepository) SetQuantity(_ context.Context, cartID, productID string, quantity int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := productLineKey(productID)
	if quantity == 0 {
		delete(f.byCart[cartID], key)
		return nil
	}
	f.ensureCart(cartID)
	f.byCart[cartID][key] = &domain.CartItem{CartID: cartID, ProductID: productID, Quantity: quantity}
	return nil
}

func (f *fakeCartItemRepository) SetQuantityForVariant(_ context.Context, cartID, productID, variantID string, quantity int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := variantLineKey(variantID)
	if quantity == 0 {
		delete(f.byCart[cartID], key)
		return nil
	}
	f.ensureCart(cartID)
	vid := variantID
	f.byCart[cartID][key] = &domain.CartItem{CartID: cartID, ProductID: productID, VariantID: &vid, Quantity: quantity}
	return nil
}

func (f *fakeCartItemRepository) Remove(_ context.Context, cartID, productID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byCart[cartID], productLineKey(productID))
	return nil
}

func (f *fakeCartItemRepository) RemoveVariant(_ context.Context, cartID, variantID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byCart[cartID], variantLineKey(variantID))
	return nil
}

func (f *fakeCartItemRepository) ListByCart(_ context.Context, cartID string) ([]*domain.CartItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []*domain.CartItem
	for _, item := range f.byCart[cartID] {
		copyItem := *item
		items = append(items, &copyItem)
	}
	return items, nil
}

func (f *fakeCartItemRepository) Clear(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byCart, cartID)
	return nil
}

func (f *fakeCartItemRepository) ensureCart(cartID string) {
	if f.byCart[cartID] == nil {
		f.byCart[cartID] = make(map[string]*domain.CartItem)
	}
}

type fakeCatalogGateway struct {
	products map[string]*adapter.ProductInfo
	variants map[string]*adapter.VariantInfo
}

func newFakeCatalogGateway() *fakeCatalogGateway {
	return &fakeCatalogGateway{products: make(map[string]*adapter.ProductInfo), variants: make(map[string]*adapter.VariantInfo)}
}

func (f *fakeCatalogGateway) GetProduct(_ context.Context, productID string) (*adapter.ProductInfo, error) {
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
