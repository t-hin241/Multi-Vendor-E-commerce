package usecase

import (
	"context"
	"time"

	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

// TxRunner runs fn in one database transaction; repository calls made with
// the ctx passed to fn join it.
type TxRunner interface {
	Run(ctx context.Context, fn func(context.Context) error) error
}

type CartRepositoryPort interface {
	GetOrCreateForUser(ctx context.Context, userID string) (*domain.Cart, error)
	LockForUser(ctx context.Context, userID string) (*domain.Cart, error)
	BumpVersion(ctx context.Context, cartID string) (int64, error)
}

type CartItemRepositoryPort interface {
	ListByCart(ctx context.Context, cartID string) ([]*domain.CartItem, error)
	ListByIDs(ctx context.Context, cartID string, ids []string) ([]*domain.CartItem, error)
	FindLine(ctx context.Context, cartID, productID string, variantID *string) (*domain.CartItem, error)
	CountLines(ctx context.Context, cartID string) (int, error)
	HasOtherCurrency(ctx context.Context, cartID, currency string, exceptLineID *string) (bool, error)
	Insert(ctx context.Context, item *domain.CartItem) error
	Update(ctx context.Context, item *domain.CartItem) error
	Delete(ctx context.Context, cartID, lineID string) (bool, error)
	DeleteAll(ctx context.Context, cartID string) (int64, error)
}

type CheckoutOperationRepositoryPort interface {
	Find(ctx context.Context, operationID string) (*domain.CheckoutOperation, error)
	Insert(ctx context.Context, op *domain.CheckoutOperation) error
	MarkConsumed(ctx context.Context, operationID, hash string, receipt *domain.ConsumeReceipt) error
}

type RetentionRepositoryPort interface {
	PurgeIdleCarts(ctx context.Context, idleBefore time.Time, limit int) (int64, error)
	PurgeOperations(ctx context.Context, createdBefore time.Time, limit int) (int64, error)
}

// CatalogGateway lets the use case validate a product (or variant) is
// sellable and read its current price/name/labels for display, without
// owning any product data.
type CatalogGateway interface {
	GetProduct(ctx context.Context, productID string) (*adapter.ProductInfo, error)
	GetVariant(ctx context.Context, variantID string) (*adapter.VariantInfo, error)
}

// InventoryGateway reads current stock for display-only warnings.
type InventoryGateway interface {
	GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error)
	GetProductStock(ctx context.Context, productID string) (quantity int64, exists bool, err error)
}
