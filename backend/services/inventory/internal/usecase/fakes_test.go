package usecase_test

import (
	"context"
	"strconv"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
)

type fakeItemRepository struct {
	mu        sync.Mutex
	byProduct map[string]*domain.InventoryItem
	byVariant map[string]*domain.InventoryItem
	nextID    int
}

func newFakeItemRepository() *fakeItemRepository {
	return &fakeItemRepository{byProduct: make(map[string]*domain.InventoryItem), byVariant: make(map[string]*domain.InventoryItem)}
}

func (f *fakeItemRepository) Create(_ context.Context, item *domain.InventoryItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if item.VariantID != nil {
		if _, exists := f.byVariant[*item.VariantID]; exists {
			return repository.ErrItemAlreadyExists
		}
	} else if _, exists := f.byProduct[item.ProductID]; exists {
		return repository.ErrItemAlreadyExists
	}
	f.nextID++
	item.ID = "item-" + strconv.Itoa(f.nextID)
	item.CreatedAt = time.Now()
	item.UpdatedAt = time.Now()
	stored := *item
	if item.VariantID != nil {
		f.byVariant[*item.VariantID] = &stored
	} else {
		f.byProduct[item.ProductID] = &stored
	}
	return nil
}

func (f *fakeItemRepository) FindByProductID(_ context.Context, productID string) (*domain.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.byProduct[productID]
	if !ok {
		return nil, repository.ErrItemNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (f *fakeItemRepository) FindByVariantID(_ context.Context, variantID string) (*domain.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.byVariant[variantID]
	if !ok {
		return nil, repository.ErrItemNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (f *fakeItemRepository) ListByVendor(_ context.Context, vendorID string, _, _ int) ([]*domain.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.InventoryItem
	for _, item := range f.byProduct {
		if item.VendorID == vendorID {
			copyItem := *item
			out = append(out, &copyItem)
		}
	}
	for _, item := range f.byVariant {
		if item.VendorID == vendorID {
			copyItem := *item
			out = append(out, &copyItem)
		}
	}
	return out, nil
}

func (f *fakeItemRepository) Restock(_ context.Context, productID string, quantity int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.byProduct[productID]
	if !ok {
		return &repository.ErrProductNotStocked{ProductID: productID}
	}
	item.AvailableQuantity += quantity
	return nil
}

func (f *fakeItemRepository) RestockVariant(_ context.Context, variantID string, quantity int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.byVariant[variantID]
	if !ok {
		return &repository.ErrProductNotStocked{ProductID: variantID}
	}
	item.AvailableQuantity += quantity
	return nil
}

func (f *fakeItemRepository) ListByVariantIDs(_ context.Context, variantIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(variantIDs))
	for _, id := range variantIDs {
		if item, ok := f.byVariant[id]; ok {
			out[id] = item.AvailableQuantity
		}
	}
	return out, nil
}

// fakeReservationRepository mirrors the real repository's atomic
// reserve/release semantics (all-or-nothing, no oversell) against the same
// fakeItemRepository, so the use case's error-mapping can be exercised
// without a live database.
type fakeReservationRepository struct {
	mu    sync.Mutex
	items *fakeItemRepository
}

func newFakeReservationRepository(items *fakeItemRepository) *fakeReservationRepository {
	return &fakeReservationRepository{items: items}
}

func (f *fakeReservationRepository) itemFor(line domain.ReservationLine) (*domain.InventoryItem, bool) {
	if line.VariantID != nil {
		item, ok := f.items.byVariant[*line.VariantID]
		return item, ok
	}
	item, ok := f.items.byProduct[line.ProductID]
	return item, ok
}

func (f *fakeReservationRepository) ReserveAtomic(_ context.Context, orderID string, lines []domain.ReservationLine) ([]*domain.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, line := range lines {
		item, ok := f.itemFor(line)
		if !ok {
			return nil, &repository.ErrProductNotStocked{ProductID: line.ProductID}
		}
		if item.AvailableQuantity < line.Quantity {
			return nil, &repository.ErrInsufficientStock{ProductID: line.ProductID, Available: item.AvailableQuantity, Requested: line.Quantity}
		}
	}

	var reservations []*domain.Reservation
	for _, line := range lines {
		item, _ := f.itemFor(line)
		item.AvailableQuantity -= line.Quantity
		item.ReservedQuantity += line.Quantity
		reservations = append(reservations, &domain.Reservation{
			ProductID: line.ProductID, VariantID: line.VariantID, OrderID: orderID, Quantity: line.Quantity, Status: domain.ReservationActive,
		})
	}
	return reservations, nil
}

func (f *fakeReservationRepository) ReleaseByOrderID(_ context.Context, _ string) error {
	return nil
}

func (f *fakeReservationRepository) CommitByOrderID(_ context.Context, _ string) error {
	return nil
}

type fakeVendorGateway struct {
	approvedVendors map[string]string
}

func newFakeVendorGateway() *fakeVendorGateway {
	return &fakeVendorGateway{approvedVendors: make(map[string]string)}
}

func (f *fakeVendorGateway) GetApprovedVendorID(_ context.Context, userID, vendorID string) (string, error) {
	approved, ok := f.approvedVendors[userID]
	if !ok || approved != vendorID {
		return "", apperror.Forbidden("You must have an approved vendor account to manage stock")
	}
	return vendorID, nil
}

type fakeVariantOwner struct {
	VendorID  string
	ProductID string
}

type fakeCatalogGateway struct {
	productOwners   map[string]string
	variantOwners   map[string]fakeVariantOwner
	productStatuses map[string]string
}

func newFakeCatalogGateway() *fakeCatalogGateway {
	return &fakeCatalogGateway{
		productOwners:   make(map[string]string),
		variantOwners:   make(map[string]fakeVariantOwner),
		productStatuses: make(map[string]string),
	}
}

func (f *fakeCatalogGateway) GetProductOwnerVendorID(_ context.Context, productID string) (string, error) {
	vendorID, ok := f.productOwners[productID]
	if !ok {
		return "", apperror.NotFound("Product not found")
	}
	return vendorID, nil
}

func (f *fakeCatalogGateway) GetVariantOwner(_ context.Context, variantID string) (string, string, error) {
	owner, ok := f.variantOwners[variantID]
	if !ok {
		return "", "", apperror.NotFound("Variant not found")
	}
	return owner.VendorID, owner.ProductID, nil
}

// GetProductStatus defaults to "" (not approved) for a product with no
// explicit entry, matching a real not-yet-approved product.
func (f *fakeCatalogGateway) GetProductStatus(_ context.Context, productID string) (string, error) {
	return f.productStatuses[productID], nil
}

// fakeRestockRequestRepository mirrors RestockRequestRepository in memory,
// enough to exercise the use case's create/list/decide flow without a live
// database.
type fakeRestockRequestRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.RestockRequest
	nextID int
}

func newFakeRestockRequestRepository() *fakeRestockRequestRepository {
	return &fakeRestockRequestRepository{byID: make(map[string]*domain.RestockRequest)}
}

func (f *fakeRestockRequestRepository) Create(_ context.Context, req *domain.RestockRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	req.ID = "restock-request-" + strconv.Itoa(f.nextID)
	req.Status = domain.RestockPending
	req.CreatedAt = time.Now()
	req.UpdatedAt = time.Now()
	stored := *req
	f.byID[req.ID] = &stored
	return nil
}

func (f *fakeRestockRequestRepository) FindByID(_ context.Context, id string) (*domain.RestockRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrRestockRequestNotFound
	}
	copyReq := *req
	return &copyReq, nil
}

func (f *fakeRestockRequestRepository) ListByStatus(_ context.Context, status string, _, _ int) ([]*domain.RestockRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.RestockRequest
	for _, req := range f.byID {
		if status == "" || string(req.Status) == status {
			copyReq := *req
			out = append(out, &copyReq)
		}
	}
	return out, nil
}

func (f *fakeRestockRequestRepository) ListByVendor(_ context.Context, vendorID string, _, _ int) ([]*domain.RestockRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.RestockRequest
	for _, req := range f.byID {
		if req.VendorID == vendorID {
			copyReq := *req
			out = append(out, &copyReq)
		}
	}
	return out, nil
}

func (f *fakeRestockRequestRepository) UpdateStatus(_ context.Context, id string, status domain.RestockStatus, adminUserID string, reason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.byID[id]
	if !ok {
		return repository.ErrRestockRequestNotFound
	}
	req.Status = status
	req.RejectionReason = reason
	req.DecidedBy = &adminUserID
	now := time.Now()
	req.DecidedAt = &now
	return nil
}
