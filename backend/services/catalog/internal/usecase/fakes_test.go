package usecase_test

import (
	"context"
	"strconv"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

type fakeCategoryRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.Category
	nextID int
}

func newFakeCategoryRepository() *fakeCategoryRepository {
	return &fakeCategoryRepository{byID: make(map[string]*domain.Category)}
}

func (f *fakeCategoryRepository) Create(_ context.Context, c *domain.Category) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	c.ID = "category-" + strconv.Itoa(f.nextID)
	c.CreatedAt = time.Now()
	stored := *c
	f.byID[c.ID] = &stored
	return nil
}

func (f *fakeCategoryRepository) FindByID(_ context.Context, id string) (*domain.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrCategoryNotFound
	}
	copyC := *c
	return &copyC, nil
}

func (f *fakeCategoryRepository) SlugExists(_ context.Context, slug string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.byID {
		if c.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeCategoryRepository) List(_ context.Context) ([]*domain.Category, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Category
	for _, c := range f.byID {
		copyC := *c
		out = append(out, &copyC)
	}
	return out, nil
}

// seed adds a level-1 (main) category directly, bypassing Create, for test setup.
func (f *fakeCategoryRepository) seed(id, name, slug string) {
	f.byID[id] = &domain.Category{ID: id, Name: name, Slug: slug, Level: 1, CreatedAt: time.Now()}
}

// seedChild adds a category with an explicit parent/level directly, bypassing
// Create, for hierarchy-specific test setup.
func (f *fakeCategoryRepository) seedChild(id, name, slug, parentID string, level int) {
	f.byID[id] = &domain.Category{ID: id, Name: name, Slug: slug, ParentID: &parentID, Level: level, CreatedAt: time.Now()}
}

type fakeProductRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.Product
	nextID int
}

func newFakeProductRepository() *fakeProductRepository {
	return &fakeProductRepository{byID: make(map[string]*domain.Product)}
}

func (f *fakeProductRepository) Create(_ context.Context, p *domain.Product) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	p.ID = "product-" + strconv.Itoa(f.nextID)
	p.Status = domain.StatusPendingReview
	p.IsActive = true
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	stored := *p
	f.byID[p.ID] = &stored
	return nil
}

func (f *fakeProductRepository) SlugExists(_ context.Context, slug string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.byID {
		if p.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeProductRepository) FindByID(_ context.Context, id string) (*domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrProductNotFound
	}
	copyP := *p
	return &copyP, nil
}

func (f *fakeProductRepository) FindBySlug(_ context.Context, slug string) (*domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.byID {
		if p.Slug == slug {
			copyP := *p
			return &copyP, nil
		}
	}
	return nil, repository.ErrProductNotFound
}

func (f *fakeProductRepository) ListByVendor(_ context.Context, vendorID string, _, _ int) ([]*domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Product
	for _, p := range f.byID {
		if p.VendorID == vendorID {
			copyP := *p
			out = append(out, &copyP)
		}
	}
	return out, nil
}

func (f *fakeProductRepository) ListByStatus(_ context.Context, status string, _, _ int) ([]*domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Product
	for _, p := range f.byID {
		if status == "" || string(p.Status) == status {
			copyP := *p
			out = append(out, &copyP)
		}
	}
	return out, nil
}

func (f *fakeProductRepository) ListStorefront(_ context.Context, categoryID, search string, _, _ int) ([]*domain.Product, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Product
	for _, p := range f.byID {
		if !p.IsPubliclyVisible() {
			continue
		}
		if categoryID != "" && p.CategoryID != categoryID {
			continue
		}
		copyP := *p
		out = append(out, &copyP)
	}
	return out, nil
}

func (f *fakeProductRepository) UpdateStatus(_ context.Context, id string, status domain.Status, reason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return repository.ErrProductNotFound
	}
	p.Status = status
	p.RejectionReason = reason
	return nil
}

func (f *fakeProductRepository) UpdateActive(_ context.Context, id string, isActive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return repository.ErrProductNotFound
	}
	p.IsActive = isActive
	return nil
}

type fakeProductImageRepository struct {
	mu     sync.Mutex
	byID   map[string][]*domain.ProductImage
	nextID int
}

func newFakeProductImageRepository() *fakeProductImageRepository {
	return &fakeProductImageRepository{byID: make(map[string][]*domain.ProductImage)}
}

func (f *fakeProductImageRepository) ReplaceForProduct(_ context.Context, img *domain.ProductImage) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var oldKeys []string
	for _, existing := range f.byID[img.ProductID] {
		oldKeys = append(oldKeys, existing.ObjectKey)
	}

	f.nextID++
	img.ID = "image-" + strconv.Itoa(f.nextID)
	img.CreatedAt = time.Now()
	img.Position = 0
	f.byID[img.ProductID] = []*domain.ProductImage{img}
	return oldKeys, nil
}

func (f *fakeProductImageRepository) ListForProduct(_ context.Context, productID string) ([]*domain.ProductImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byID[productID], nil
}

func (f *fakeProductImageRepository) ListForProducts(_ context.Context, productIDs []string) (map[string]*domain.ProductImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]*domain.ProductImage, len(productIDs))
	for _, id := range productIDs {
		if images := f.byID[id]; len(images) > 0 {
			out[id] = images[0]
		}
	}
	return out, nil
}

type fakeProductMediaRepository struct {
	mu     sync.Mutex
	byID   map[string][]*domain.ProductMedia
	nextID int
}

func newFakeProductMediaRepository() *fakeProductMediaRepository {
	return &fakeProductMediaRepository{byID: make(map[string][]*domain.ProductMedia)}
}

func (f *fakeProductMediaRepository) Create(_ context.Context, m *domain.ProductMedia) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m.ID = "media-" + strconv.Itoa(f.nextID)
	m.CreatedAt = time.Now()
	f.byID[m.ProductID] = append(f.byID[m.ProductID], m)
	return nil
}

func (f *fakeProductMediaRepository) CountForProduct(_ context.Context, productID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID[productID]), nil
}

func (f *fakeProductMediaRepository) ListForProduct(_ context.Context, productID string) ([]*domain.ProductMedia, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byID[productID], nil
}

type fakeAuditLogRepository struct {
	mu      sync.Mutex
	entries []string
}

func newFakeAuditLogRepository() *fakeAuditLogRepository {
	return &fakeAuditLogRepository{}
}

func (f *fakeAuditLogRepository) Create(_ context.Context, productID, actorUserID, action string, _ *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, action+":"+productID+":"+actorUserID)
	return nil
}

// fakeVendorGateway simulates Vendor's internal approval-status endpoint.
// approvedVendors maps a userID to the vendorID it owns, once approved.
type fakeVendorGateway struct {
	approvedVendors map[string]string
}

func newFakeVendorGateway() *fakeVendorGateway {
	return &fakeVendorGateway{approvedVendors: make(map[string]string)}
}

func (f *fakeVendorGateway) GetApprovedVendorID(_ context.Context, userID string) (string, error) {
	vendorID, ok := f.approvedVendors[userID]
	if !ok {
		return "", apperror.Forbidden("You must have an approved vendor account to sell products")
	}
	return vendorID, nil
}

type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted []string
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: make(map[string][]byte)}
}

func (f *fakeObjectStore) Upload(_ context.Context, objectKey string, data []byte, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[objectKey] = data
	return "http://minio.local/product-images/" + objectKey, nil
}

func (f *fakeObjectStore) Delete(_ context.Context, objectKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, objectKey)
	f.deleted = append(f.deleted, objectKey)
	return nil
}

// fakeVendorNameGateway simulates Vendor's batch shop-name lookup. Set err
// to simulate the call failing outright; names holds whatever the "live"
// service would resolve (an id absent from names is simply unresolved by
// Vendor, distinct from the whole call failing).
type fakeVendorNameGateway struct {
	names map[string]string
	err   error
}

func newFakeVendorNameGateway() *fakeVendorNameGateway {
	return &fakeVendorNameGateway{names: make(map[string]string)}
}

func (f *fakeVendorNameGateway) GetShopNames(_ context.Context, vendorIDs []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]string, len(vendorIDs))
	for _, id := range vendorIDs {
		if name, ok := f.names[id]; ok {
			out[id] = name
		}
	}
	return out, nil
}

// fakeOrderGateway simulates Order's batch quantity-sold lookup, same
// failure-simulation shape as fakeVendorNameGateway.
type fakeOrderGateway struct {
	quantities map[string]int64
	err        error
}

func newFakeOrderGateway() *fakeOrderGateway {
	return &fakeOrderGateway{quantities: make(map[string]int64)}
}

func (f *fakeOrderGateway) GetQuantitySold(_ context.Context, productIDs []string) (map[string]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]int64, len(productIDs))
	for _, id := range productIDs {
		if qty, ok := f.quantities[id]; ok {
			out[id] = qty
		}
	}
	return out, nil
}

// fakeStorefrontCacheRepository is Catalog's own local fallback store, kept
// as simple in-memory maps mirroring the two Postgres cache tables.
type fakeStorefrontCacheRepository struct {
	mu           sync.Mutex
	vendorNames  map[string]string
	quantitySold map[string]int64
	variantStock map[string]int64
}

func newFakeStorefrontCacheRepository() *fakeStorefrontCacheRepository {
	return &fakeStorefrontCacheRepository{
		vendorNames:  make(map[string]string),
		quantitySold: make(map[string]int64),
		variantStock: make(map[string]int64),
	}
}

func (f *fakeStorefrontCacheRepository) UpsertVendorNames(_ context.Context, entries map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, name := range entries {
		f.vendorNames[id] = name
	}
	return nil
}

func (f *fakeStorefrontCacheRepository) GetVendorNames(_ context.Context, vendorIDs []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(vendorIDs))
	for _, id := range vendorIDs {
		if name, ok := f.vendorNames[id]; ok {
			out[id] = name
		}
	}
	return out, nil
}

func (f *fakeStorefrontCacheRepository) UpsertQuantitySold(_ context.Context, entries map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, qty := range entries {
		f.quantitySold[id] = qty
	}
	return nil
}

func (f *fakeStorefrontCacheRepository) GetQuantitySold(_ context.Context, productIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(productIDs))
	for _, id := range productIDs {
		if qty, ok := f.quantitySold[id]; ok {
			out[id] = qty
		}
	}
	return out, nil
}

func (f *fakeStorefrontCacheRepository) UpsertVariantStock(_ context.Context, entries map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, qty := range entries {
		f.variantStock[id] = qty
	}
	return nil
}

func (f *fakeStorefrontCacheRepository) GetVariantStock(_ context.Context, variantIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(variantIDs))
	for _, id := range variantIDs {
		if qty, ok := f.variantStock[id]; ok {
			out[id] = qty
		}
	}
	return out, nil
}

// fakeInventoryGateway simulates Inventory's batch variant-stock lookup for
// the public product-detail page, same failure-simulation shape as
// fakeOrderGateway/fakeVendorNameGateway.
type fakeInventoryGateway struct {
	stock map[string]int64
	err   error
}

func newFakeInventoryGateway() *fakeInventoryGateway {
	return &fakeInventoryGateway{stock: make(map[string]int64)}
}

func (f *fakeInventoryGateway) GetVariantStock(_ context.Context, variantIDs []string) (map[string]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]int64, len(variantIDs))
	for _, id := range variantIDs {
		if qty, ok := f.stock[id]; ok {
			out[id] = qty
		}
	}
	return out, nil
}

// fakeAttributeTemplateResolver simulates AttributeUseCase.ResolveTemplate
// for ProductUseCase's tests, without exercising the real inheritance/merge
// logic (that's covered directly in attribute_usecase_test.go). templates
// is keyed by category id; err, if set, simulates the resolve call failing.
type fakeAttributeTemplateResolver struct {
	templates map[string][]domain.ResolvedAttribute
	err       error
}

func newFakeAttributeTemplateResolver() *fakeAttributeTemplateResolver {
	return &fakeAttributeTemplateResolver{templates: make(map[string][]domain.ResolvedAttribute)}
}

func (f *fakeAttributeTemplateResolver) ResolveTemplate(_ context.Context, categoryID string) ([]domain.ResolvedAttribute, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.templates[categoryID], nil
}

type fakeProductAttributeValueRepository struct {
	mu        sync.Mutex
	byProduct map[string][]*domain.ProductAttributeValue
	nextID    int
}

func newFakeProductAttributeValueRepository() *fakeProductAttributeValueRepository {
	return &fakeProductAttributeValueRepository{byProduct: make(map[string][]*domain.ProductAttributeValue)}
}

func (f *fakeProductAttributeValueRepository) ReplaceForProduct(_ context.Context, productID string, values []*domain.ProductAttributeValue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := make([]*domain.ProductAttributeValue, 0, len(values))
	for _, v := range values {
		f.nextID++
		copyV := *v
		copyV.ID = "attr-value-" + strconv.Itoa(f.nextID)
		copyV.ProductID = productID
		copyV.CreatedAt = time.Now()
		stored = append(stored, &copyV)
	}
	f.byProduct[productID] = stored
	return nil
}

func (f *fakeProductAttributeValueRepository) ListForProduct(_ context.Context, productID string) ([]*domain.ProductAttributeValue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byProduct[productID], nil
}

// fakeProductVariantRepository mirrors ProductVariantRepository's error
// semantics (duplicate sku / duplicate option combination) in memory.
type fakeProductVariantRepository struct {
	mu          sync.Mutex
	byID        map[string]*domain.ProductVariant
	skus        map[string]bool
	variantKeys map[string]bool // "productID|variantKey" -> exists
	optionsByID map[string][]domain.VariantOptionSelection
	nextID      int
}

func newFakeProductVariantRepository() *fakeProductVariantRepository {
	return &fakeProductVariantRepository{
		byID:        make(map[string]*domain.ProductVariant),
		skus:        make(map[string]bool),
		variantKeys: make(map[string]bool),
		optionsByID: make(map[string][]domain.VariantOptionSelection),
	}
}

func (f *fakeProductVariantRepository) Create(_ context.Context, v *domain.ProductVariant, selections []domain.VariantOptionSelection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.skus[v.SKU] {
		return repository.ErrSKUTaken
	}
	key := v.ProductID + "|" + v.VariantKey
	if f.variantKeys[key] {
		return repository.ErrVariantAlreadyExists
	}
	f.nextID++
	v.ID = "variant-" + strconv.Itoa(f.nextID)
	v.CreatedAt = time.Now()
	stored := *v
	f.byID[v.ID] = &stored
	f.skus[v.SKU] = true
	f.variantKeys[key] = true
	f.optionsByID[v.ID] = append([]domain.VariantOptionSelection{}, selections...)
	return nil
}

func (f *fakeProductVariantRepository) FindByID(_ context.Context, id string) (*domain.ProductVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrVariantNotFound
	}
	copyV := *v
	return &copyV, nil
}

func (f *fakeProductVariantRepository) HasVariantsForProduct(_ context.Context, productID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.byID {
		if v.ProductID == productID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeProductVariantRepository) ListForProduct(_ context.Context, productID string) ([]*domain.ProductVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.ProductVariant
	for _, v := range f.byID {
		if v.ProductID == productID {
			copyV := *v
			out = append(out, &copyV)
		}
	}
	return out, nil
}

func (f *fakeProductVariantRepository) ListOptionsForVariants(_ context.Context, variantIDs []string) (map[string][]domain.VariantOptionSelection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]domain.VariantOptionSelection, len(variantIDs))
	for _, id := range variantIDs {
		out[id] = f.optionsByID[id]
	}
	return out, nil
}

type fakeAttributeRepository struct {
	mu      sync.Mutex
	byID    map[string]*domain.Attribute
	options map[string][]*domain.AttributeOption
	nextID  int
}

func newFakeAttributeRepository() *fakeAttributeRepository {
	return &fakeAttributeRepository{byID: make(map[string]*domain.Attribute), options: make(map[string][]*domain.AttributeOption)}
}

func (f *fakeAttributeRepository) Create(_ context.Context, a *domain.Attribute) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.byID {
		if existing.Code == a.Code {
			return repository.ErrAttributeCodeTaken
		}
	}
	f.nextID++
	a.ID = "attribute-" + strconv.Itoa(f.nextID)
	a.IsActive = true
	a.CreatedAt = time.Now()
	stored := *a
	f.byID[a.ID] = &stored
	return nil
}

func (f *fakeAttributeRepository) FindByID(_ context.Context, id string) (*domain.Attribute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrAttributeNotFound
	}
	copyA := *a
	return &copyA, nil
}

func (f *fakeAttributeRepository) List(_ context.Context) ([]*domain.Attribute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Attribute
	for _, a := range f.byID {
		copyA := *a
		out = append(out, &copyA)
	}
	return out, nil
}

func (f *fakeAttributeRepository) ListByIDs(_ context.Context, ids []string) (map[string]*domain.Attribute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]*domain.Attribute, len(ids))
	for _, id := range ids {
		if a, ok := f.byID[id]; ok {
			copyA := *a
			out[id] = &copyA
		}
	}
	return out, nil
}

func (f *fakeAttributeRepository) CountOptionsForAttribute(_ context.Context, attributeID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.options[attributeID]), nil
}

func (f *fakeAttributeRepository) AddOption(_ context.Context, o *domain.AttributeOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.options[o.AttributeID] {
		if existing.Value == o.Value {
			return repository.ErrOptionValueTaken
		}
	}
	f.nextID++
	o.ID = "option-" + strconv.Itoa(f.nextID)
	o.CreatedAt = time.Now()
	stored := *o
	f.options[o.AttributeID] = append(f.options[o.AttributeID], &stored)
	return nil
}

func (f *fakeAttributeRepository) ListOptionsForAttributes(_ context.Context, attributeIDs []string) (map[string][]*domain.AttributeOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]*domain.AttributeOption, len(attributeIDs))
	for _, id := range attributeIDs {
		out[id] = f.options[id]
	}
	return out, nil
}

func (f *fakeAttributeRepository) ListOptionsByIDs(_ context.Context, ids []string) (map[string]*domain.AttributeOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	out := make(map[string]*domain.AttributeOption, len(ids))
	for _, opts := range f.options {
		for _, o := range opts {
			if wanted[o.ID] {
				out[o.ID] = o
			}
		}
	}
	return out, nil
}

// seed adds an attribute directly, bypassing Create, for test setup.
func (f *fakeAttributeRepository) seed(id, code, name string, dataType domain.DataType) {
	f.byID[id] = &domain.Attribute{ID: id, Code: code, Name: name, DataType: dataType, IsActive: true, CreatedAt: time.Now()}
}

// seedVariantAxis adds a select attribute marked variant-defining directly,
// bypassing Create, for variant-related test setup.
func (f *fakeAttributeRepository) seedVariantAxis(id, code, name string) {
	f.byID[id] = &domain.Attribute{ID: id, Code: code, Name: name, DataType: domain.DataTypeSelect, IsActive: true, IsVariantDefining: true, CreatedAt: time.Now()}
}

// seedOption adds an option directly, bypassing AddOption, for test setup.
func (f *fakeAttributeRepository) seedOption(id, attributeID, value string) {
	f.options[attributeID] = append(f.options[attributeID], &domain.AttributeOption{ID: id, AttributeID: attributeID, Value: value, CreatedAt: time.Now()})
}

type fakeCategoryAttributeRuleRepository struct {
	mu     sync.Mutex
	rules  []*domain.CategoryAttributeRule
	nextID int
}

func newFakeCategoryAttributeRuleRepository() *fakeCategoryAttributeRuleRepository {
	return &fakeCategoryAttributeRuleRepository{}
}

func (f *fakeCategoryAttributeRuleRepository) CurrentVersion(_ context.Context, categoryID, attributeID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := 0
	for _, r := range f.rules {
		if r.CategoryID == categoryID && r.AttributeID == attributeID && r.Version > version {
			version = r.Version
		}
	}
	return version, nil
}

func (f *fakeCategoryAttributeRuleRepository) Insert(_ context.Context, rule *domain.CategoryAttributeRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	rule.ID = "rule-" + strconv.Itoa(f.nextID)
	rule.CreatedAt = time.Now()
	stored := *rule
	f.rules = append(f.rules, &stored)
	return nil
}

// CurrentRulesForCategories mirrors the real repository's "highest version
// per (category, attribute) pair" semantics over the in-memory insert-only
// log, so tests can exercise the same "current = latest" contract.
func (f *fakeCategoryAttributeRuleRepository) CurrentRulesForCategories(_ context.Context, categoryIDs []string) ([]*domain.CategoryAttributeRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wanted := make(map[string]bool, len(categoryIDs))
	for _, id := range categoryIDs {
		wanted[id] = true
	}
	latest := make(map[string]*domain.CategoryAttributeRule)
	for _, r := range f.rules {
		if !wanted[r.CategoryID] {
			continue
		}
		key := r.CategoryID + "|" + r.AttributeID
		if existing, ok := latest[key]; !ok || r.Version > existing.Version {
			copyR := *r
			latest[key] = &copyR
		}
	}
	out := make([]*domain.CategoryAttributeRule, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	return out, nil
}

type fakeProductPackagingRepository struct {
	mu   sync.Mutex
	byID map[string]domain.Packaging
}

func newFakeProductPackagingRepository() *fakeProductPackagingRepository {
	return &fakeProductPackagingRepository{byID: map[string]domain.Packaging{}}
}

func (f *fakeProductPackagingRepository) Upsert(_ context.Context, productID string, p domain.Packaging) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[productID] = p
	return nil
}

func (f *fakeProductPackagingRepository) Get(_ context.Context, productID string) (domain.Packaging, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byID[productID], nil
}

func (f *fakeProductPackagingRepository) ListByProductIDs(_ context.Context, productIDs []string) (map[string]domain.Packaging, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]domain.Packaging, len(productIDs))
	for _, id := range productIDs {
		if p, ok := f.byID[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}
