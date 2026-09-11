package usecase

import (
	"context"

	"shopee/backend/services/inventory/internal/domain"
)

type InventoryItemRepositoryPort interface {
	Create(ctx context.Context, item *domain.InventoryItem) error
	FindByProductID(ctx context.Context, productID string) (*domain.InventoryItem, error)
	FindByVariantID(ctx context.Context, variantID string) (*domain.InventoryItem, error)
	ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.InventoryItem, error)
	ListByVariantIDs(ctx context.Context, variantIDs []string) (map[string]int64, error)
	Restock(ctx context.Context, productID string, quantity int64) error
	RestockVariant(ctx context.Context, variantID string, quantity int64) error
}

type ReservationRepositoryPort interface {
	ReserveAtomic(ctx context.Context, orderID string, lines []domain.ReservationLine) ([]*domain.Reservation, error)
	ReleaseByOrderID(ctx context.Context, orderID string) error
	CommitByOrderID(ctx context.Context, orderID string) error
}

// VendorGateway lets the use case check vendor approval without owning any
// vendor data itself.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID string) (vendorID string, err error)
}

// CatalogGateway lets the use case verify product (or variant) ownership
// without owning any product data itself.
type CatalogGateway interface {
	GetProductOwnerVendorID(ctx context.Context, productID string) (vendorID string, err error)
	// GetVariantOwner resolves the vendor and product a variant belongs to,
	// so a variant-scoped stock request never has to trust a client-supplied
	// product_id for ownership purposes.
	GetVariantOwner(ctx context.Context, variantID string) (vendorID, productID string, err error)
}
