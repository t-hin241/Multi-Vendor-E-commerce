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
	ListStorefront(ctx context.Context, categoryID, vendorID, search string, limit, offset int) ([]*domain.Product, error)
	CountStorefront(ctx context.Context, categoryID, vendorID, search string) (int, error)
	UpdateStatus(ctx context.Context, id string, status domain.Status, rejectionReason *string) error
	UpdateActive(ctx context.Context, id string, isActive bool) error
}

type ProductImageRepositoryPort interface {
	// ReplaceForProduct atomically swaps a product's single main image:
	// deletes every existing row for the product and inserts img in one
	// transaction, so the product never has zero or two images at once.
	// Returns the object keys of whatever was deleted, for storage cleanup.
	ReplaceForProduct(ctx context.Context, img *domain.ProductImage) (deletedObjectKeys []string, err error)
	// DeleteForProduct removes a product's main image entirely (no
	// replacement) — returns the object keys removed, for storage cleanup.
	DeleteForProduct(ctx context.Context, productID string) (deletedObjectKeys []string, err error)
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
	List(ctx context.Context, productID string) ([]*domain.AuditLog, error)
}

// ObjectStore is the subset of pkg/platform/objectstorage.Client the use
// case needs, so tests can fake it instead of talking to MinIO.
type ObjectStore interface {
	Upload(ctx context.Context, objectKey string, data []byte, contentType string) (url string, err error)
	Delete(ctx context.Context, objectKey string) error
}

// VendorGateway confirms a user owns a specific, approved vendor (shop)
// without Catalog owning any vendor data itself; see internal/adapter for
// the HTTP implementation. A user may own several shops (1:N), so callers
// always name which one they're acting as — this only validates that name.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error)
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
	// LookupAttributeLabels resolves attribute names / option values directly
	// by id, independent of category_attribute_rules — the fallback path for
	// a persisted variant selection whose attribute isn't (or is no longer)
	// part of the category's current rule-driven template, so display never
	// degrades to a raw id.
	LookupAttributeLabels(ctx context.Context, attributeIDs, optionIDs []string) (map[string]*domain.Attribute, map[string]*domain.AttributeOption, error)
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
	// CheckStockReadiness confirms initial stock has been set up before a
	// draft product can be submitted for review: if variantIDs is empty, a
	// plain product-level stock record with quantity > 0; otherwise, a
	// stocked record for every listed variant.
	CheckStockReadiness(ctx context.Context, productID string, variantIDs []string) (bool, error)
	// GetProductStock resolves current stock for a non-variant product, for
	// the admin moderation detail view. ok is false when no inventory row
	// exists yet.
	GetProductStock(ctx context.Context, productID string) (quantity int64, ok bool, err error)
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
