package usecase

import (
	"context"

	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

type CartRepositoryPort interface {
	GetOrCreateForUser(ctx context.Context, userID string) (*domain.Cart, error)
}

type CartItemRepositoryPort interface {
	AddQuantity(ctx context.Context, cartID, productID string, delta int64) error
	AddQuantityForVariant(ctx context.Context, cartID, productID, variantID string, delta int64) error
	SetQuantity(ctx context.Context, cartID, productID string, quantity int64) error
	SetQuantityForVariant(ctx context.Context, cartID, productID, variantID string, quantity int64) error
	Remove(ctx context.Context, cartID, productID string) error
	RemoveVariant(ctx context.Context, cartID, variantID string) error
	ListByCart(ctx context.Context, cartID string) ([]*domain.CartItem, error)
	Clear(ctx context.Context, cartID string) error
}

// CatalogGateway lets the use case validate a product (or variant) is
// sellable and read its current price/name/labels for display, without
// owning any product data.
type CatalogGateway interface {
	GetProduct(ctx context.Context, productID string) (*adapter.ProductInfo, error)
	GetVariant(ctx context.Context, variantID string) (*adapter.VariantInfo, error)
}
