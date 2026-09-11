package usecase_test

import (
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

type productTestFixture struct {
	products          *usecase.ProductUseCase
	categories        *fakeCategoryRepository
	vendors           *fakeVendorGateway
	audit             *fakeAuditLogRepository
	store             *fakeObjectStore
	vendorNames       *fakeVendorNameGateway
	orders            *fakeOrderGateway
	storefrontCache   *fakeStorefrontCacheRepository
	attributeTemplate *fakeAttributeTemplateResolver
	attributeValues   *fakeProductAttributeValueRepository
	variants          *fakeProductVariantRepository
	inventory         *fakeInventoryGateway
	packaging         *fakeProductPackagingRepository
}

func newProductFixture() *productTestFixture {
	categories := newFakeCategoryRepository()
	categories.seed("cat-1", "Shoes", "shoes")

	vendors := newFakeVendorGateway()
	audit := newFakeAuditLogRepository()
	store := newFakeObjectStore()
	vendorNames := newFakeVendorNameGateway()
	orders := newFakeOrderGateway()
	storefrontCache := newFakeStorefrontCacheRepository()
	attributeTemplate := newFakeAttributeTemplateResolver()
	attributeValues := newFakeProductAttributeValueRepository()
	variants := newFakeProductVariantRepository()
	inventory := newFakeInventoryGateway()
	packaging := newFakeProductPackagingRepository()

	products := usecase.NewProductUseCase(
		newFakeProductRepository(),
		newFakeProductImageRepository(),
		newFakeProductMediaRepository(),
		categories,
		audit,
		vendors,
		store,
		vendorNames,
		orders,
		storefrontCache,
		attributeTemplate,
		attributeValues,
		variants,
		inventory,
		packaging,
	)

	return &productTestFixture{
		products:          products,
		categories:        categories,
		vendors:           vendors,
		audit:             audit,
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

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func TestCreate_RejectsUnapprovedVendor(t *testing.T) {
	f := newProductFixture()

	_, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestCreate_RejectsUnknownCategory(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"

	_, err := f.products.Create(t.Context(), "user-1", "no-such-category", "Sneakers", "desc", 100000, nil)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestCreate_SucceedsAsPendingReview(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"

	p, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status != "pending_review" {
		t.Errorf("expected pending_review, got %q", p.Status)
	}
	if p.VendorID != "vendor-1" {
		t.Errorf("expected vendor id to come from the vendor gateway, got %q", p.VendorID)
	}
}

func TestCreate_RejectsMissingRequiredAttribute(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{Attribute: domain.Attribute{ID: "attr-1", Name: "Material", DataType: domain.DataTypeText}, RuleID: "rule-1", Required: true},
	}

	_, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a missing required attribute, got %v", appErr.Code)
	}
}

func TestCreate_StoresSubmittedAttributeValues(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{
			Attribute: domain.Attribute{ID: "attr-1", Name: "Color", DataType: domain.DataTypeSelect},
			Options:   []domain.AttributeOption{{ID: "opt-red", Value: "Red"}, {ID: "opt-blue", Value: "Blue"}},
			RuleID:    "rule-1",
			Required:  true,
		},
	}
	submitted := []usecase.AttributeValueInput{{AttributeID: "attr-1", OptionIDs: []string{"opt-red"}}}

	p, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, submitted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	values := f.attributeValues.byProduct[p.ID]
	if len(values) != 1 || values[0].OptionID == nil || *values[0].OptionID != "opt-red" {
		t.Fatalf("expected the selected option to be stored, got %+v", values)
	}
	if values[0].RuleID == nil || *values[0].RuleID != "rule-1" {
		t.Errorf("expected the resolved rule id to be stamped on the value, got %v", values[0].RuleID)
	}
}

func TestCreate_RejectsOptionNotBelongingToAttribute(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{
			Attribute: domain.Attribute{ID: "attr-1", Name: "Color", DataType: domain.DataTypeSelect},
			Options:   []domain.AttributeOption{{ID: "opt-red", Value: "Red"}},
			RuleID:    "rule-1",
		},
	}
	submitted := []usecase.AttributeValueInput{{AttributeID: "attr-1", OptionIDs: []string{"opt-not-listed"}}}

	_, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, submitted)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an option that doesn't belong to the attribute, got %v", appErr.Code)
	}
}

func TestCreate_RoutesPackagingFieldsToProductPackaging(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{Attribute: domain.Attribute{ID: "attr-weight", Code: "pkg_weight", Name: "Package weight", DataType: domain.DataTypeNumber}, RuleID: "rule-1", Required: true},
		{Attribute: domain.Attribute{ID: "attr-length", Code: "pkg_length", Name: "Package length", DataType: domain.DataTypeNumber}, RuleID: "rule-2", Required: false},
	}
	weight, length := "500", "200"
	submitted := []usecase.AttributeValueInput{
		{AttributeID: "attr-weight", Value: &weight},
		{AttributeID: "attr-length", Value: &length},
	}

	p, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, submitted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if values := f.attributeValues.byProduct[p.ID]; len(values) != 0 {
		t.Errorf("expected packaging fields to not be stored as generic attribute values, got %+v", values)
	}
	pkg, _ := f.packaging.Get(t.Context(), p.ID)
	if pkg.WeightGrams == nil || *pkg.WeightGrams != 500 {
		t.Errorf("expected weight_grams=500, got %v", pkg.WeightGrams)
	}
	if pkg.LengthMM == nil || *pkg.LengthMM != 200 {
		t.Errorf("expected length_mm=200, got %v", pkg.LengthMM)
	}
}

func TestCreate_RejectsMissingRequiredPackagingField(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{Attribute: domain.Attribute{ID: "attr-weight", Code: "pkg_weight", Name: "Package weight", DataType: domain.DataTypeNumber}, RuleID: "rule-1", Required: true},
	}

	_, err := f.products.Create(t.Context(), "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a missing required packaging field, got %v", appErr.Code)
	}
}

func TestGetPublicBySlug_HidesUnapprovedProduct(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, _, _, _, _, err = f.products.GetPublicBySlug(ctx, p.Slug)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("a pending_review product must not be publicly visible, got %v", appErr.Code)
	}
}

func TestGetPublicBySlug_ShowsApprovedProduct(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}

	found, _, _, _, _, _, err := f.products.GetPublicBySlug(ctx, p.Slug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != p.ID {
		t.Errorf("expected to find the approved product, got a different one")
	}
}

func TestGetPublicBySlug_IncludesVariantsWithLiveStock(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	seedSizeAxisTemplate(f, "cat-1")

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	v, _, err := f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S", []string{"opt-s"})
	if err != nil {
		t.Fatalf("unexpected error creating variant: %v", err)
	}
	f.inventory.stock[v.ID] = 7

	_, _, _, _, variants, degraded, err := f.products.GetPublicBySlug(ctx, p.Slug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if degraded {
		t.Errorf("expected no degradation when the inventory gateway succeeds")
	}
	if len(variants) != 1 || variants[0].AvailableQuantity != 7 {
		t.Fatalf("expected one variant with available quantity 7, got %+v", variants)
	}
	if variants[0].Options[0].OptionValue != "S" {
		t.Errorf("expected the resolved option label S, got %+v", variants[0].Options)
	}
}

func TestGetPublicBySlug_FallsBackToCacheWhenInventoryFails(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	seedSizeAxisTemplate(f, "cat-1")

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	v, _, err := f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S", []string{"opt-s"})
	if err != nil {
		t.Fatalf("unexpected error creating variant: %v", err)
	}
	f.storefrontCache.variantStock[v.ID] = 3
	f.inventory.err = errors.New("inventory service unreachable")

	_, _, _, _, variants, degraded, err := f.products.GetPublicBySlug(ctx, p.Slug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !degraded {
		t.Errorf("expected stockInfoDegraded to be true when the live inventory call fails")
	}
	if len(variants) != 1 || variants[0].AvailableQuantity != 3 {
		t.Fatalf("expected the cached stock value 3 as a fallback, got %+v", variants)
	}
}

func TestGetPublicBySlug_IncludesMedia(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	uploaded, err := f.products.UploadMedia(ctx, "user-1", p.ID, "video/mp4", []byte("fake-mp4-bytes"))
	if err != nil {
		t.Fatalf("unexpected error uploading media: %v", err)
	}

	_, _, media, _, _, _, err := f.products.GetPublicBySlug(ctx, p.Slug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(media) != 1 || media[0].ID != uploaded.ID {
		t.Errorf("expected the uploaded media item to be returned, got %+v", media)
	}
}

func TestApprove_OnlyPendingReviewCanBeApproved(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}

	_, err = f.products.Approve(ctx, p.ID, "admin-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict re-approving, got %v", appErr.Code)
	}
	if len(f.audit.entries) != 1 {
		t.Errorf("expected exactly one audit entry, got %d", len(f.audit.entries))
	}
}

func TestListForModeration_ReturnsPendingReviewProducts(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	products, err := f.products.ListForModeration(ctx, "pending_review", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(products) != 1 || products[0].ID != p.ID {
		t.Fatalf("expected the newly created product to be returned, got %+v", products)
	}
}

func TestListForModeration_RejectsInvalidStatus(t *testing.T) {
	f := newProductFixture()

	_, err := f.products.ListForModeration(t.Context(), "pending", 20, 0)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for unknown status, got %v", appErr.Code)
	}
}

func TestListStorefront_EnrichesWithVendorNamesAndSales(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	f.vendorNames.names["vendor-1"] = "Sneaker Shop"
	f.orders.quantities[p.ID] = 42

	products, _, vendorNames, quantitySold, vendorDegraded, salesDegraded, err := f.products.ListStorefront(ctx, "", "", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(products) != 1 || products[0].ID != p.ID {
		t.Fatalf("expected the approved product to be returned, got %+v", products)
	}
	if vendorNames["vendor-1"] != "Sneaker Shop" {
		t.Errorf("expected vendor name to be resolved, got %q", vendorNames["vendor-1"])
	}
	if quantitySold[p.ID] != 42 {
		t.Errorf("expected quantity sold to be resolved, got %d", quantitySold[p.ID])
	}
	if vendorDegraded || salesDegraded {
		t.Errorf("expected no degradation when both gateways succeed, got vendorDegraded=%v salesDegraded=%v", vendorDegraded, salesDegraded)
	}
}

func TestListStorefront_FallsBackToCacheOnVendorGatewayError(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	f.storefrontCache.vendorNames["vendor-1"] = "Last Known Shop Name"
	f.vendorNames.err = errors.New("vendor service unreachable")

	_, _, vendorNames, _, vendorDegraded, _, err := f.products.ListStorefront(ctx, "", "", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendorNames["vendor-1"] != "Last Known Shop Name" {
		t.Errorf("expected the cached vendor name as a fallback, got %q", vendorNames["vendor-1"])
	}
	if !vendorDegraded {
		t.Errorf("expected vendorInfoDegraded to be true when the live vendor call fails")
	}
}

func TestListStorefront_TreatsUnsoldProductAsZeroNotDegraded(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	// f.orders.quantities intentionally left empty: the live call succeeds
	// but has nothing to report for this product.

	_, _, _, quantitySold, _, salesDegraded, err := f.products.ListStorefront(ctx, "", "", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if quantitySold[p.ID] != 0 {
		t.Errorf("expected an unsold product to report 0, got %d", quantitySold[p.ID])
	}
	if salesDegraded {
		t.Errorf("expected no degradation for a legitimately unsold product")
	}
}

func TestUploadImage_RejectsNonOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.vendors.approvedVendors["user-2"] = "vendor-2"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.UploadImage(ctx, "user-2", p.ID, "image/jpeg", []byte("fake-jpeg-bytes"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner upload, got %v", appErr.Code)
	}
}

func TestUploadImage_SucceedsForOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	img, err := f.products.UploadImage(ctx, "user-1", p.ID, "image/jpeg", []byte("fake-jpeg-bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.Position != 0 {
		t.Errorf("expected the first image to be at position 0, got %d", img.Position)
	}
	if len(f.store.objects) != 1 {
		t.Errorf("expected exactly one object uploaded, got %d", len(f.store.objects))
	}
}

func TestUploadImage_ReplacesExistingImage(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	first, err := f.products.UploadImage(ctx, "user-1", p.ID, "image/jpeg", []byte("first"))
	if err != nil {
		t.Fatalf("unexpected error uploading first image: %v", err)
	}
	second, err := f.products.UploadImage(ctx, "user-1", p.ID, "image/jpeg", []byte("second"))
	if err != nil {
		t.Fatalf("unexpected error uploading second image: %v", err)
	}
	if second.Position != 0 {
		t.Errorf("expected the replacement image to be at position 0, got %d", second.Position)
	}

	images, err := f.products.ListImagesForOwner(ctx, "user-1", p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 1 || images[0].ID != second.ID {
		t.Fatalf("expected only the replacement image to remain, got %+v", images)
	}
	if _, stillStored := f.store.objects[first.ObjectKey]; stillStored {
		t.Errorf("expected the first image's object to be deleted from storage")
	}
}

func TestListImagesForOwner_RejectsNonOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.vendors.approvedVendors["user-2"] = "vendor-2"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.ListImagesForOwner(ctx, "user-2", p.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner listing, got %v", appErr.Code)
	}
}

func TestListImagesForOwner_ReturnsUploadedItem(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.UploadImage(ctx, "user-1", p.ID, "image/jpeg", []byte("fake-jpeg-bytes")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	images, err := f.products.ListImagesForOwner(ctx, "user-1", p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 1 {
		t.Errorf("expected one image, got %d", len(images))
	}
}

func TestUploadImage_RejectsBadContentType(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.UploadImage(ctx, "user-1", p.ID, "application/pdf", []byte("not-an-image"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestUploadMedia_RejectsNonOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.vendors.approvedVendors["user-2"] = "vendor-2"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.UploadMedia(ctx, "user-2", p.ID, "video/mp4", []byte("fake-mp4-bytes"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner upload, got %v", appErr.Code)
	}
}

func TestUploadMedia_SucceedsForOwnerImage(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, err := f.products.UploadMedia(ctx, "user-1", p.ID, "image/jpeg", []byte("fake-jpeg-bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Kind != domain.MediaKindImage {
		t.Errorf("expected kind image, got %q", m.Kind)
	}
	if m.ContentType != "image/jpeg" {
		t.Errorf("expected content type image/jpeg, got %q", m.ContentType)
	}
	if m.SizeBytes != int64(len("fake-jpeg-bytes")) {
		t.Errorf("expected size_bytes to match the payload length, got %d", m.SizeBytes)
	}
	if m.Position != 0 {
		t.Errorf("expected the first media item to be at position 0, got %d", m.Position)
	}
}

func TestUploadMedia_SucceedsForOwnerVideo(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, err := f.products.UploadMedia(ctx, "user-1", p.ID, "video/webm", []byte("fake-webm-bytes"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Kind != domain.MediaKindVideo {
		t.Errorf("expected kind video, got %q", m.Kind)
	}
	if len(f.store.objects) != 1 {
		t.Errorf("expected exactly one object uploaded, got %d", len(f.store.objects))
	}
}

func TestUploadMedia_SecondItemGetsNextPosition(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := f.products.UploadMedia(ctx, "user-1", p.ID, "image/jpeg", []byte("first")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := f.products.UploadMedia(ctx, "user-1", p.ID, "video/mp4", []byte("second"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.Position != 1 {
		t.Errorf("expected the second media item to be at position 1, got %d", second.Position)
	}
}

func TestUploadMedia_RejectsBadContentType(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.UploadMedia(ctx, "user-1", p.ID, "application/pdf", []byte("not-media"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestUploadMedia_RejectsOversizedVideo(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oversized := make([]byte, 21*1024*1024)
	_, err = f.products.UploadMedia(ctx, "user-1", p.ID, "video/mp4", oversized)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an oversized video, got %v", appErr.Code)
	}
}

func TestUploadMedia_RejectsSixthItem(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := f.products.UploadMedia(ctx, "user-1", p.ID, "image/jpeg", []byte("item")); err != nil {
			t.Fatalf("unexpected error uploading item %d: %v", i, err)
		}
	}

	_, err = f.products.UploadMedia(ctx, "user-1", p.ID, "image/jpeg", []byte("sixth"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a sixth media item, got %v", appErr.Code)
	}
}

func TestListMediaForOwner_RejectsNonOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.vendors.approvedVendors["user-2"] = "vendor-2"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.products.ListMediaForOwner(ctx, "user-2", p.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner, got %v", appErr.Code)
	}
}

func TestListMediaForOwner_ReturnsUploadedItems(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()

	p, err := f.products.Create(ctx, "user-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.products.UploadMedia(ctx, "user-1", p.ID, "image/jpeg", []byte("fake-jpeg-bytes")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	media, err := f.products.ListMediaForOwner(ctx, "user-1", p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(media) != 1 {
		t.Errorf("expected one media item, got %d", len(media))
	}
}
