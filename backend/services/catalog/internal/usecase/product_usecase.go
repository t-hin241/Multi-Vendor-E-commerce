package usecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

type ProductUseCase struct {
	products          ProductRepositoryPort
	images            ProductImageRepositoryPort
	media             ProductMediaRepositoryPort
	categories        CategoryRepositoryPort
	auditLogs         AuditLogRepositoryPort
	vendors           VendorGateway
	store             ObjectStore
	vendorNames       VendorNameGateway
	orders            OrderGateway
	storefrontCache   StorefrontCacheRepositoryPort
	attributeTemplate AttributeTemplateResolver
	attributeValues   ProductAttributeValueRepositoryPort
	variants          ProductVariantRepositoryPort
	inventory         InventoryGateway
	packaging         ProductPackagingRepositoryPort
}

func NewProductUseCase(
	products ProductRepositoryPort,
	images ProductImageRepositoryPort,
	media ProductMediaRepositoryPort,
	categories CategoryRepositoryPort,
	auditLogs AuditLogRepositoryPort,
	vendors VendorGateway,
	store ObjectStore,
	vendorNames VendorNameGateway,
	orders OrderGateway,
	storefrontCache StorefrontCacheRepositoryPort,
	attributeTemplate AttributeTemplateResolver,
	attributeValues ProductAttributeValueRepositoryPort,
	variants ProductVariantRepositoryPort,
	inventory InventoryGateway,
	packaging ProductPackagingRepositoryPort,
) *ProductUseCase {
	return &ProductUseCase{
		products:          products,
		images:            images,
		media:             media,
		categories:        categories,
		auditLogs:         auditLogs,
		vendors:           vendors,
		store:             store,
		vendorNames:       vendorNames,
		orders:            orders,
		storefrontCache:   storefrontCache,
		attributeTemplate: attributeTemplate,
		attributeValues:   attributeValues,
		variants:          variants,
		inventory:         inventory,
		packaging:         packaging,
	}
}

const defaultCurrency = "VND"

// AttributeValueInput is one submitted attribute value from a create-product
// request: Value carries a text/number/boolean field as a string (parsed
// per the attribute's data type), OptionIDs carries the chosen option id(s)
// for select (exactly one) / multi_select (one or more) attributes.
type AttributeValueInput struct {
	AttributeID string
	Value       *string
	OptionIDs   []string
}

func (uc *ProductUseCase) Create(ctx context.Context, userID, categoryID, name, description string, priceAmount int64, submittedAttributes []AttributeValueInput) (*domain.Product, error) {
	if err := domain.ValidateProductInput(name, description, priceAmount); err != nil {
		return nil, err
	}
	if err := uc.validateCategory(ctx, categoryID); err != nil {
		return nil, err
	}

	// Resolved before creating the product, so a bad/missing required
	// attribute is rejected before anything is written.
	resolved, err := uc.attributeTemplate.ResolveTemplate(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	attributeValues, packaging, err := buildAttributeValues(resolved, submittedAttributes)
	if err != nil {
		return nil, err
	}

	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	slug, err := uniqueSlug(ctx, name, uc.products.SlugExists)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	p := &domain.Product{
		VendorID:    vendorID,
		CategoryID:  categoryID,
		Name:        name,
		Slug:        slug,
		Description: description,
		PriceAmount: priceAmount,
		Currency:    defaultCurrency,
	}

	if err := uc.products.Create(ctx, p); err != nil {
		return nil, apperror.Internal(err)
	}

	if err := uc.attributeValues.ReplaceForProduct(ctx, p.ID, attributeValues); err != nil {
		return nil, apperror.Internal(err)
	}

	if !packaging.IsEmpty() {
		if err := uc.packaging.Upsert(ctx, p.ID, packaging); err != nil {
			return nil, apperror.Internal(err)
		}
	}

	return p, nil
}

// buildAttributeValues validates submitted values against the category's
// resolved template (required-check, data-type parsing, option-membership
// check) and builds the rows to persist. The template is always re-resolved
// server-side (see Create) — a client can't smuggle in an unresolved
// attribute id or a stale rule id. The four reserved packaging codes
// (pkg_weight/pkg_length/pkg_width/pkg_height) go through the exact same
// required/optional validation as any other attribute, but are routed into
// the returned Packaging value instead of the generic attribute-value list
// — see domain.IsPackagingCode.
func buildAttributeValues(resolved []domain.ResolvedAttribute, submitted []AttributeValueInput) ([]*domain.ProductAttributeValue, domain.Packaging, error) {
	submittedByAttribute := make(map[string]AttributeValueInput, len(submitted))
	for _, s := range submitted {
		submittedByAttribute[s.AttributeID] = s
	}

	var values []*domain.ProductAttributeValue
	var packaging domain.Packaging
	for _, r := range resolved {
		// Variant-defining attributes are never captured as a flat product-
		// level value: their value is expressed per-variant instead, via
		// CreateVariant/product_variant_options. Requiring one here too
		// would force a value before any variant even exists.
		if r.Attribute.IsVariantDefining {
			continue
		}

		input, hasInput := submittedByAttribute[r.Attribute.ID]
		isEmpty := !hasInput || (input.Value == nil && len(input.OptionIDs) == 0)
		if isEmpty {
			if r.Required {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s is required", r.Attribute.Name))
			}
			continue
		}

		if domain.IsPackagingCode(r.Attribute.Code) {
			if input.Value == nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be a number", r.Attribute.Name))
			}
			num, err := strconv.ParseFloat(*input.Value, 64)
			if err != nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be a number", r.Attribute.Name))
			}
			whole := int64(num)
			switch domain.PackagingAttributeCode(r.Attribute.Code) {
			case domain.PackagingCodeWeight:
				packaging.WeightGrams = &whole
			case domain.PackagingCodeLength:
				packaging.LengthMM = &whole
			case domain.PackagingCodeWidth:
				packaging.WidthMM = &whole
			case domain.PackagingCodeHeight:
				packaging.HeightMM = &whole
			}
			continue
		}

		ruleID := r.RuleID
		switch r.Attribute.DataType {
		case domain.DataTypeText:
			if input.Value == nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be a text value", r.Attribute.Name))
			}
			values = append(values, &domain.ProductAttributeValue{AttributeID: r.Attribute.ID, RuleID: &ruleID, ValueText: input.Value})

		case domain.DataTypeNumber:
			if input.Value == nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be a number", r.Attribute.Name))
			}
			num, err := strconv.ParseFloat(*input.Value, 64)
			if err != nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be a number", r.Attribute.Name))
			}
			values = append(values, &domain.ProductAttributeValue{AttributeID: r.Attribute.ID, RuleID: &ruleID, ValueNumber: &num})

		case domain.DataTypeBoolean:
			if input.Value == nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be true or false", r.Attribute.Name))
			}
			b, err := strconv.ParseBool(*input.Value)
			if err != nil {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s must be true or false", r.Attribute.Name))
			}
			values = append(values, &domain.ProductAttributeValue{AttributeID: r.Attribute.ID, RuleID: &ruleID, ValueBoolean: &b})

		case domain.DataTypeSelect:
			if len(input.OptionIDs) != 1 {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s requires exactly one option", r.Attribute.Name))
			}
			optionID := input.OptionIDs[0]
			if !optionBelongsTo(r.Options, optionID) {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("Invalid option for %s", r.Attribute.Name))
			}
			values = append(values, &domain.ProductAttributeValue{AttributeID: r.Attribute.ID, RuleID: &ruleID, OptionID: &optionID})

		case domain.DataTypeMultiSelect:
			if len(input.OptionIDs) == 0 {
				return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("%s requires at least one option", r.Attribute.Name))
			}
			for _, optionID := range input.OptionIDs {
				if !optionBelongsTo(r.Options, optionID) {
					return nil, domain.Packaging{}, apperror.Validation(fmt.Sprintf("Invalid option for %s", r.Attribute.Name))
				}
				values = append(values, &domain.ProductAttributeValue{AttributeID: r.Attribute.ID, RuleID: &ruleID, OptionID: &optionID})
			}
		}
	}

	return values, packaging, nil
}

func optionBelongsTo(options []domain.AttributeOption, optionID string) bool {
	for _, o := range options {
		if o.ID == optionID {
			return true
		}
	}
	return false
}

// GetPublicBySlug is the public product-detail page's read model: the
// product itself plus its images/media/attribute values, and — for a
// product with variants — each variant resolved to its display labels and
// current stock (live from Inventory, with the same last-known-value
// cache fallback used for vendor names/units-sold on the storefront
// listing, flagged via stockInfoDegraded).
func (uc *ProductUseCase) GetPublicBySlug(ctx context.Context, slug string) (
	p *domain.Product,
	images []*domain.ProductImage,
	media []*domain.ProductMedia,
	attributeValues []*domain.ProductAttributeValue,
	variants []domain.VariantView,
	stockInfoDegraded bool,
	err error,
) {
	p, err = uc.products.FindBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, nil, nil, nil, nil, false, apperror.NotFound("Product not found")
		}
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}
	if !p.IsPubliclyVisible() {
		return nil, nil, nil, nil, nil, false, apperror.NotFound("Product not found")
	}

	images, err = uc.images.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}
	media, err = uc.media.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}
	attributeValues, err = uc.attributeValues.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}

	variantRows, err := uc.variants.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}
	if len(variantRows) == 0 {
		return p, images, media, attributeValues, nil, false, nil
	}

	variantIDs := make([]string, 0, len(variantRows))
	for _, v := range variantRows {
		variantIDs = append(variantIDs, v.ID)
	}
	optionsByVariant, err := uc.variants.ListOptionsForVariants(ctx, variantIDs)
	if err != nil {
		return nil, nil, nil, nil, nil, false, apperror.Internal(err)
	}
	resolved, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
	if err != nil {
		return nil, nil, nil, nil, nil, false, err
	}
	resolvedByAttribute := indexResolvedByAttribute(resolved)
	stock, degraded := uc.resolveVariantStock(ctx, variantIDs)

	variants = make([]domain.VariantView, 0, len(variantRows))
	for _, v := range variantRows {
		variants = append(variants, domain.VariantView{
			Variant:           *v,
			Options:           resolveVariantOptionDetails(resolvedByAttribute, optionsByVariant[v.ID]),
			AvailableQuantity: stock[v.ID],
		})
	}

	return p, images, media, attributeValues, variants, degraded, nil
}

// ListStorefront is the public product grid's read model. Besides the
// products themselves, it enriches each with its main image, its vendor's
// shop name and its all-time units-sold count — both cross-service facts
// fetched live from Vendor and Order, with Catalog's own local
// storefrontCache as a last-known-value fallback if either call fails, so
// the listing itself never fails just because a dependency is briefly
// down. The two degraded flags tell the caller when that fallback was used,
// so the frontend can show a "may be outdated" notice without hiding or
// blocking the product grid.
func (uc *ProductUseCase) ListStorefront(ctx context.Context, categoryID, search string, limit, offset int) (
	products []*domain.Product,
	images map[string]*domain.ProductImage,
	vendorNames map[string]string,
	quantitySold map[string]int64,
	vendorInfoDegraded bool,
	salesInfoDegraded bool,
	err error,
) {
	products, err = uc.products.ListStorefront(ctx, categoryID, search, limit, offset)
	if err != nil {
		return nil, nil, nil, nil, false, false, apperror.Internal(err)
	}
	if len(products) == 0 {
		return products, map[string]*domain.ProductImage{}, map[string]string{}, map[string]int64{}, false, false, nil
	}

	productIDs := make([]string, 0, len(products))
	vendorIDSet := make(map[string]struct{}, len(products))
	for _, p := range products {
		productIDs = append(productIDs, p.ID)
		vendorIDSet[p.VendorID] = struct{}{}
	}
	vendorIDs := make([]string, 0, len(vendorIDSet))
	for id := range vendorIDSet {
		vendorIDs = append(vendorIDs, id)
	}

	images, err = uc.images.ListForProducts(ctx, productIDs)
	if err != nil {
		return nil, nil, nil, nil, false, false, apperror.Internal(err)
	}

	vendorNames, vendorInfoDegraded = uc.resolveVendorNames(ctx, vendorIDs)
	quantitySold, salesInfoDegraded = uc.resolveQuantitySold(ctx, productIDs)

	return products, images, vendorNames, quantitySold, vendorInfoDegraded, salesInfoDegraded, nil
}

// resolveVendorNames prefers the live Vendor lookup. If the call fails
// outright, every requested id falls back to the cache. If it succeeds but
// some individual vendor ids come back unresolved (unexpected — vendors are
// never hard-deleted, see Vendor service's own audit-trail rule), those
// specific ids are backfilled from the cache too. Only a gap the cache
// can't fill counts as degraded.
func (uc *ProductUseCase) resolveVendorNames(ctx context.Context, vendorIDs []string) (map[string]string, bool) {
	live, err := uc.vendorNames.GetShopNames(ctx, vendorIDs)
	if err != nil {
		cached, _ := uc.storefrontCache.GetVendorNames(ctx, vendorIDs)
		return cached, true
	}

	_ = uc.storefrontCache.UpsertVendorNames(ctx, live) // best-effort refresh of the fallback

	missing := idsNotIn(vendorIDs, live)
	if len(missing) == 0 {
		return live, false
	}
	cachedForMissing, cacheErr := uc.storefrontCache.GetVendorNames(ctx, missing)
	if cacheErr == nil {
		for id, name := range cachedForMissing {
			live[id] = name
		}
	}
	return live, len(idsNotIn(missing, cachedForMissing)) > 0
}

// resolveQuantitySold prefers the live Order lookup. Unlike vendor names,
// an id absent from a *successful* response is a normal, expected case (the
// product simply has no sales yet) — not something to fall back to the
// cache for, and not degraded. The cache fallback only kicks in when the
// live call fails entirely.
func (uc *ProductUseCase) resolveQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, bool) {
	live, err := uc.orders.GetQuantitySold(ctx, productIDs)
	if err == nil {
		_ = uc.storefrontCache.UpsertQuantitySold(ctx, live) // best-effort refresh of the fallback
		return live, false
	}
	cached, _ := uc.storefrontCache.GetQuantitySold(ctx, productIDs)
	return cached, true
}

// resolveVariantStock mirrors resolveQuantitySold exactly: a variant absent
// from a *successful* live response has no inventory row yet (not stocked,
// a legitimate zero), while a total call failure falls back to the cache
// for every requested variant and reports degraded.
func (uc *ProductUseCase) resolveVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, bool) {
	live, err := uc.inventory.GetVariantStock(ctx, variantIDs)
	if err == nil {
		_ = uc.storefrontCache.UpsertVariantStock(ctx, live) // best-effort refresh of the fallback
		return live, false
	}
	cached, _ := uc.storefrontCache.GetVariantStock(ctx, variantIDs)
	return cached, true
}

func idsNotIn[V any](ids []string, present map[string]V) []string {
	var missing []string
	for _, id := range ids {
		if _, ok := present[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// GetByIDForOwnerLookup exists for service-to-service ownership checks
// (Inventory verifying a vendor owns the product before touching its
// stock; Cart/Order checking sellability and whether a variant must be
// selected). It returns the product regardless of moderation status.
func (uc *ProductUseCase) GetByIDForOwnerLookup(ctx context.Context, productID string) (product *domain.Product, hasVariants bool, packageWeightGrams *int64, err error) {
	product, err = uc.findForDecision(ctx, productID)
	if err != nil {
		return nil, false, nil, err
	}
	hasVariants, err = uc.variants.HasVariantsForProduct(ctx, productID)
	if err != nil {
		return nil, false, nil, apperror.Internal(err)
	}
	pkg, err := uc.packaging.Get(ctx, productID)
	if err != nil {
		return nil, false, nil, apperror.Internal(err)
	}
	return product, hasVariants, pkg.WeightGrams, nil
}

func (uc *ProductUseCase) ListMine(ctx context.Context, userID string, limit, offset int) ([]*domain.Product, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	products, err := uc.products.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return products, nil
}

func (uc *ProductUseCase) SetActive(ctx context.Context, userID, productID string, isActive bool) (*domain.Product, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, err
	}

	if err := uc.products.UpdateActive(ctx, p.ID, isActive); err != nil {
		return nil, apperror.Internal(err)
	}
	p.IsActive = isActive
	return p, nil
}

func (uc *ProductUseCase) UploadImage(ctx context.Context, userID, productID, contentType string, data []byte) (*domain.ProductImage, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, err
	}

	ext, err := domain.ValidateImageUpload(contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}

	suffix, err := randomHex(16)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	objectKey := fmt.Sprintf("products/%s/%s%s", p.ID, suffix, ext)

	url, err := uc.store.Upload(ctx, objectKey, data, contentType)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	img := &domain.ProductImage{ProductID: p.ID, ObjectKey: objectKey, URL: url}
	oldKeys, err := uc.images.ReplaceForProduct(ctx, img)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	// Best-effort: the DB row (just swapped atomically above) is the source
	// of truth for "the product's image", so a storage-delete failure here
	// only leaves an orphaned blob, not an inconsistency — same tolerance
	// this codebase already applies to secondary side effects like
	// notification sends failing without rolling back the main transaction.
	for _, key := range oldKeys {
		_ = uc.store.Delete(ctx, key)
	}

	return img, nil
}

// ListImagesForOwner serves the vendor's own view of a product's single
// main image, mirroring ListMediaForOwner, so their console can preview
// what's currently uploaded right after replacing it.
func (uc *ProductUseCase) ListImagesForOwner(ctx context.Context, userID, productID string) ([]*domain.ProductImage, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, err
	}

	images, err := uc.images.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return images, nil
}

// UploadMedia adds one item to a product's extended-description media
// gallery — a separate concept from UploadImage's plain photo gallery, that
// also accepts short videos. Mirrors UploadImage's ownership-then-validate
// flow exactly.
func (uc *ProductUseCase) UploadMedia(ctx context.Context, userID, productID, contentType string, data []byte) (*domain.ProductMedia, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, err
	}

	kind, ext, err := domain.ValidateMediaUpload(contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}

	position, err := uc.media.CountForProduct(ctx, p.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if position >= domain.MaxMediaItemsPerProduct {
		return nil, apperror.Validation("A product can have at most 5 media items")
	}

	suffix, err := randomHex(16)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	objectKey := fmt.Sprintf("products/%s/media/%s%s", p.ID, suffix, ext)

	url, err := uc.store.Upload(ctx, objectKey, data, contentType)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	m := &domain.ProductMedia{
		ProductID: p.ID, Kind: kind, ObjectKey: objectKey, URL: url,
		ContentType: contentType, SizeBytes: int64(len(data)), Position: position,
	}
	if err := uc.media.Create(ctx, m); err != nil {
		return nil, apperror.Internal(err)
	}
	return m, nil
}

// ListMediaForOwner serves the vendor's own view of a product's media
// gallery so their console can show what's already been uploaded.
func (uc *ProductUseCase) ListMediaForOwner(ctx context.Context, userID, productID string) ([]*domain.ProductMedia, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, err
	}

	media, err := uc.media.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return media, nil
}

func (uc *ProductUseCase) ListForModeration(ctx context.Context, status string, limit, offset int) ([]*domain.Product, error) {
	if status != "" {
		switch domain.Status(status) {
		case domain.StatusPendingReview, domain.StatusApproved, domain.StatusRejected:
		default:
			return nil, apperror.Validation("Invalid status filter")
		}
	}

	products, err := uc.products.ListByStatus(ctx, status, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return products, nil
}

func (uc *ProductUseCase) Approve(ctx context.Context, productID, adminUserID string) (*domain.Product, error) {
	p, err := uc.findForDecision(ctx, productID)
	if err != nil {
		return nil, err
	}
	if !domain.CanTransition(p.Status, domain.StatusApproved) {
		return nil, apperror.Conflict("Only a pending_review product can be approved")
	}

	if err := uc.products.UpdateStatus(ctx, p.ID, domain.StatusApproved, nil); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.auditLogs.Create(ctx, p.ID, adminUserID, "approved", nil); err != nil {
		return nil, apperror.Internal(err)
	}

	p.Status = domain.StatusApproved
	return p, nil
}

func (uc *ProductUseCase) Reject(ctx context.Context, productID, adminUserID, reason string) (*domain.Product, error) {
	if reason == "" {
		return nil, apperror.Validation("A rejection reason is required")
	}

	p, err := uc.findForDecision(ctx, productID)
	if err != nil {
		return nil, err
	}
	if !domain.CanTransition(p.Status, domain.StatusRejected) {
		return nil, apperror.Conflict("Only a pending_review product can be rejected")
	}

	if err := uc.products.UpdateStatus(ctx, p.ID, domain.StatusRejected, &reason); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.auditLogs.Create(ctx, p.ID, adminUserID, "rejected", &reason); err != nil {
		return nil, apperror.Internal(err)
	}

	p.Status = domain.StatusRejected
	p.RejectionReason = &reason
	return p, nil
}

func (uc *ProductUseCase) ownedByUser(ctx context.Context, userID, productID string) (*domain.Product, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return nil, err
	}

	p, err := uc.products.FindByID(ctx, productID)
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, apperror.NotFound("Product not found")
		}
		return nil, apperror.Internal(err)
	}

	if p.VendorID != vendorID {
		return nil, apperror.Forbidden("You do not own this product")
	}
	return p, nil
}

// CreateVariant adds one specific option combination (e.g. Color=Red,
// Size=L) as a purchasable variant of a product, with its own SKU that
// Inventory will later track stock against independently of the product
// itself. Only attributes marked variant-defining in the product's
// resolved category template are valid axes; every such axis must get
// exactly one submitted option.
func (uc *ProductUseCase) CreateVariant(ctx context.Context, userID, productID, sku string, optionIDs []string) (*domain.ProductVariant, []domain.VariantOptionDetail, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, nil, err
	}
	if err := domain.ValidateSKU(sku); err != nil {
		return nil, nil, err
	}

	resolved, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
	if err != nil {
		return nil, nil, err
	}

	selections, err := matchVariantOptions(resolved, optionIDs)
	if err != nil {
		return nil, nil, err
	}

	v := &domain.ProductVariant{ProductID: p.ID, SKU: sku, VariantKey: domain.BuildVariantKey(selections)}
	if err := uc.variants.Create(ctx, v, selections); err != nil {
		if errors.Is(err, repository.ErrSKUTaken) {
			return nil, nil, apperror.Conflict("This SKU is already in use")
		}
		if errors.Is(err, repository.ErrVariantAlreadyExists) {
			return nil, nil, apperror.Conflict("A variant with this exact option combination already exists")
		}
		return nil, nil, apperror.Internal(err)
	}

	resolvedByAttribute := indexResolvedByAttribute(resolved)
	return v, resolveVariantOptionDetails(resolvedByAttribute, selections), nil
}

// matchVariantOptions validates that optionIDs cover exactly the product
// category's variant-defining attributes, one option per axis — no
// missing axis, no duplicate axis, no option that isn't actually one of
// that axis's choices.
func matchVariantOptions(resolved []domain.ResolvedAttribute, optionIDs []string) ([]domain.VariantOptionSelection, error) {
	axes := make(map[string]domain.ResolvedAttribute) // attributeID -> axis
	for _, r := range resolved {
		if r.Attribute.IsVariantDefining {
			axes[r.Attribute.ID] = r
		}
	}
	if len(axes) == 0 {
		return nil, apperror.Validation("This category has no variant-defining attributes")
	}

	selections := make([]domain.VariantOptionSelection, 0, len(axes))
	seenAxes := make(map[string]bool, len(axes))
	for _, optionID := range optionIDs {
		matched := false
		for attributeID, axis := range axes {
			if optionBelongsTo(axis.Options, optionID) {
				if seenAxes[attributeID] {
					return nil, apperror.Validation(fmt.Sprintf("Only one option allowed for %s", axis.Attribute.Name))
				}
				seenAxes[attributeID] = true
				selections = append(selections, domain.VariantOptionSelection{AttributeID: attributeID, OptionID: optionID})
				matched = true
				break
			}
		}
		if !matched {
			return nil, apperror.Validation("Invalid option for this product's variants")
		}
	}

	for attributeID, axis := range axes {
		if !seenAxes[attributeID] {
			return nil, apperror.Validation(fmt.Sprintf("%s is required to create a variant", axis.Attribute.Name))
		}
	}

	return selections, nil
}

// ListVariantsForOwner serves the vendor's own view of a product's
// variants, each with its option selections resolved to display labels
// (attribute/option names, not just ids), for the vendor console's
// stock-management UI.
func (uc *ProductUseCase) ListVariantsForOwner(ctx context.Context, userID, productID string) ([]*domain.ProductVariant, map[string][]domain.VariantOptionDetail, error) {
	p, err := uc.ownedByUser(ctx, userID, productID)
	if err != nil {
		return nil, nil, err
	}

	variants, err := uc.variants.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	variantIDs := make([]string, 0, len(variants))
	for _, v := range variants {
		variantIDs = append(variantIDs, v.ID)
	}
	rawOptions, err := uc.variants.ListOptionsForVariants(ctx, variantIDs)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}

	resolved, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
	if err != nil {
		return nil, nil, err
	}
	resolvedByAttribute := indexResolvedByAttribute(resolved)

	details := make(map[string][]domain.VariantOptionDetail, len(rawOptions))
	for variantID, selections := range rawOptions {
		details[variantID] = resolveVariantOptionDetails(resolvedByAttribute, selections)
	}
	return variants, details, nil
}

func indexResolvedByAttribute(resolved []domain.ResolvedAttribute) map[string]domain.ResolvedAttribute {
	out := make(map[string]domain.ResolvedAttribute, len(resolved))
	for _, r := range resolved {
		out[r.Attribute.ID] = r
	}
	return out
}

// resolveVariantOptionDetails attaches display labels to raw selections by
// looking them up in the category's resolved template; a selection whose
// attribute/option no longer resolves (e.g. removed since the variant was
// created) falls back to showing its raw id rather than failing.
func resolveVariantOptionDetails(resolvedByAttribute map[string]domain.ResolvedAttribute, selections []domain.VariantOptionSelection) []domain.VariantOptionDetail {
	details := make([]domain.VariantOptionDetail, 0, len(selections))
	for _, sel := range selections {
		detail := domain.VariantOptionDetail{AttributeID: sel.AttributeID, AttributeName: sel.AttributeID, OptionID: sel.OptionID, OptionValue: sel.OptionID}
		if axis, ok := resolvedByAttribute[sel.AttributeID]; ok {
			detail.AttributeName = axis.Attribute.Name
			for _, o := range axis.Options {
				if o.ID == sel.OptionID {
					detail.OptionValue = o.Value
					break
				}
			}
		}
		details = append(details, detail)
	}
	return details
}

// GetVariantOwner backs the internal, unauthenticated lookup Inventory uses
// to verify a vendor owns the product a variant belongs to before letting
// them manage its stock — mirroring GetByIDForOwnerLookup's trust model
// exactly, just one hop further through a variant instead of a product id.
func (uc *ProductUseCase) GetVariantOwner(ctx context.Context, variantID string) (vendorID, productID, sku string, options []domain.VariantOptionDetail, err error) {
	v, err := uc.variants.FindByID(ctx, variantID)
	if err != nil {
		if errors.Is(err, repository.ErrVariantNotFound) {
			return "", "", "", nil, apperror.NotFound("Variant not found")
		}
		return "", "", "", nil, apperror.Internal(err)
	}
	p, err := uc.findForDecision(ctx, v.ProductID)
	if err != nil {
		return "", "", "", nil, err
	}

	resolved, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
	if err != nil {
		return "", "", "", nil, err
	}
	rawOptions, err := uc.variants.ListOptionsForVariants(ctx, []string{v.ID})
	if err != nil {
		return "", "", "", nil, apperror.Internal(err)
	}
	options = resolveVariantOptionDetails(indexResolvedByAttribute(resolved), rawOptions[v.ID])

	return p.VendorID, p.ID, v.SKU, options, nil
}

func (uc *ProductUseCase) validateCategory(ctx context.Context, categoryID string) error {
	_, err := uc.categories.FindByID(ctx, categoryID)
	if err != nil {
		if errors.Is(err, repository.ErrCategoryNotFound) {
			return apperror.Validation("Category does not exist")
		}
		return apperror.Internal(err)
	}
	return nil
}

func (uc *ProductUseCase) findForDecision(ctx context.Context, productID string) (*domain.Product, error) {
	p, err := uc.products.FindByID(ctx, productID)
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, apperror.NotFound("Product not found")
		}
		return nil, apperror.Internal(err)
	}
	return p, nil
}
