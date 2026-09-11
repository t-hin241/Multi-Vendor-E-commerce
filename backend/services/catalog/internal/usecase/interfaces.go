package usecase

import (
	"context"

	"shopee/backend/services/catalog/internal/domain"
)

type CategoryRepositoryPort interface {
	Create(ctx context.Context, c *domain.Category) error
	FindByID(ctx context.Context, id string) (*domain.Category, error)
	SlugExists(ctx context.Context, slug string) (bool, error)
	List(ctx context.Context) ([]*domain.Category, error)
}

type ProductRepositoryPort interface {
	Create(ctx context.Context, p *domain.Product) error
	SlugExists(ctx context.Context, slug string) (bool, error)
	FindByID(ctx context.Context, id string) (*domain.Product, error)
	FindBySlug(ctx context.Context, slug string) (*domain.Product, error)
	ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Product, error)
	ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Product, error)
	ListStorefront(ctx context.Context, categoryID, search string, limit, offset int) ([]*domain.Product, error)
	UpdateStatus(ctx context.Context, id string, status domain.Status, rejectionReason *string) error
	UpdateActive(ctx context.Context, id string, isActive bool) error
}

type ProductImageRepositoryPort interface {
	// ReplaceForProduct atomically swaps a product's single main image:
	// deletes every existing row for the product and inserts img in one
	// transaction, so the product never has zero or two images at once.
	// Returns the object keys of whatever was deleted, for storage cleanup.
	ReplaceForProduct(ctx context.Context, img *domain.ProductImage) (deletedObjectKeys []string, err error)
	ListForProduct(ctx context.Context, productID string) ([]*domain.ProductImage, error)
	// ListForProducts batch-looks-up the main image for many products at
	// once, for the storefront listing.
	ListForProducts(ctx context.Context, productIDs []string) (map[string]*domain.ProductImage, error)
}

// ProductMediaRepositoryPort is the extended-description media gallery's
// own store — separate from ProductImageRepositoryPort's plain photo
// gallery, even though the shapes are similar.
type ProductMediaRepositoryPort interface {
	Create(ctx context.Context, m *domain.ProductMedia) error
	CountForProduct(ctx context.Context, productID string) (int, error)
	ListForProduct(ctx context.Context, productID string) ([]*domain.ProductMedia, error)
}

type AuditLogRepositoryPort interface {
	Create(ctx context.Context, productID, actorUserID, action string, reason *string) error
}

// ObjectStore is the subset of pkg/platform/objectstorage.Client the use
// case needs, so tests can fake it instead of talking to MinIO.
type ObjectStore interface {
	Upload(ctx context.Context, objectKey string, data []byte, contentType string) (url string, err error)
	Delete(ctx context.Context, objectKey string) error
}

// VendorGateway lets the use case check vendor approval without owning any
// vendor data itself; see internal/adapter for the HTTP implementation.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID string) (vendorID string, err error)
}

// VendorNameGateway resolves shop names for the storefront listing. A
// separate interface from VendorGateway (different concern, same remote
// service) so a fake can implement just what a given test needs.
type VendorNameGateway interface {
	GetShopNames(ctx context.Context, vendorIDs []string) (map[string]string, error)
}

// OrderGateway resolves units-sold for the storefront listing, without
// Catalog owning any order data itself.
type OrderGateway interface {
	GetQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, error)
}

// StorefrontCacheRepositoryPort is Catalog's own local fallback read-model
// for vendor names and sold counts — see repository.StorefrontCacheRepository
// for why it exists and how it's kept fresh.
type StorefrontCacheRepositoryPort interface {
	UpsertVendorNames(ctx context.Context, entries map[string]string) error
	GetVendorNames(ctx context.Context, vendorIDs []string) (map[string]string, error)
	UpsertQuantitySold(ctx context.Context, entries map[string]int64) error
	GetQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, error)
	UpsertVariantStock(ctx context.Context, entries map[string]int64) error
	GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error)
}

type AttributeRepositoryPort interface {
	Create(ctx context.Context, a *domain.Attribute) error
	FindByID(ctx context.Context, id string) (*domain.Attribute, error)
	List(ctx context.Context) ([]*domain.Attribute, error)
	ListByIDs(ctx context.Context, ids []string) (map[string]*domain.Attribute, error)
	CountOptionsForAttribute(ctx context.Context, attributeID string) (int, error)
	AddOption(ctx context.Context, o *domain.AttributeOption) error
	ListOptionsForAttributes(ctx context.Context, attributeIDs []string) (map[string][]*domain.AttributeOption, error)
	ListOptionsByIDs(ctx context.Context, ids []string) (map[string]*domain.AttributeOption, error)
}

type CategoryAttributeRuleRepositoryPort interface {
	CurrentVersion(ctx context.Context, categoryID, attributeID string) (int, error)
	Insert(ctx context.Context, rule *domain.CategoryAttributeRule) error
	CurrentRulesForCategories(ctx context.Context, categoryIDs []string) ([]*domain.CategoryAttributeRule, error)
}

type ProductAttributeValueRepositoryPort interface {
	ReplaceForProduct(ctx context.Context, productID string, values []*domain.ProductAttributeValue) error
	ListForProduct(ctx context.Context, productID string) ([]*domain.ProductAttributeValue, error)
}

// AttributeTemplateResolver is the narrow subset of AttributeUseCase that
// ProductUseCase needs to validate and store submitted attribute values —
// it doesn't manage attributes/options/rules themselves.
type AttributeTemplateResolver interface {
	ResolveTemplate(ctx context.Context, categoryID string) ([]domain.ResolvedAttribute, error)
}

type ProductVariantRepositoryPort interface {
	Create(ctx context.Context, v *domain.ProductVariant, selections []domain.VariantOptionSelection) error
	FindByID(ctx context.Context, id string) (*domain.ProductVariant, error)
	HasVariantsForProduct(ctx context.Context, productID string) (bool, error)
	ListForProduct(ctx context.Context, productID string) ([]*domain.ProductVariant, error)
	ListOptionsForVariants(ctx context.Context, variantIDs []string) (map[string][]domain.VariantOptionSelection, error)
}

// InventoryGateway resolves current stock for a set of variants, for the
// public product-detail page. A separate concern from OrderGateway
// (quantity SOLD historically) — this is quantity currently AVAILABLE.
type InventoryGateway interface {
	GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error)
}

// ProductPackagingRepositoryPort stores each product's shipping
// weight/dimensions — a dedicated table rather than
// ProductAttributeValueRepositoryPort, since it's exactly 4 fixed numeric
// columns that Order/Shipment need to read reliably, not an arbitrary
// attribute value.
type ProductPackagingRepositoryPort interface {
	Upsert(ctx context.Context, productID string, p domain.Packaging) error
	Get(ctx context.Context, productID string) (domain.Packaging, error)
	ListByProductIDs(ctx context.Context, productIDs []string) (map[string]domain.Packaging, error)
}
