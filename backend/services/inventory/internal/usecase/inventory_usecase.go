// Package usecase orchestrates Inventory's workflows: a vendor setting up
// and restocking their own product's stock, and the internal
// reserve/release contract Order uses at checkout and on cancellation.
package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
)

type InventoryUseCase struct {
	items           InventoryItemRepositoryPort
	reservations    ReservationRepositoryPort
	restockRequests RestockRequestRepositoryPort
	vendors         VendorGateway
	catalog         CatalogGateway
}

func NewInventoryUseCase(
	items InventoryItemRepositoryPort,
	reservations ReservationRepositoryPort,
	restockRequests RestockRequestRepositoryPort,
	vendors VendorGateway,
	catalog CatalogGateway,
) *InventoryUseCase {
	return &InventoryUseCase{items: items, reservations: reservations, restockRequests: restockRequests, vendors: vendors, catalog: catalog}
}

// CreateItem sets a product's (or, if variantID is given, one specific
// variant's) initial stock. Exactly one of productID/variantID must be
// set; a variant-scoped call never trusts productID for ownership — see
// verifyItemOwnership.
func (uc *InventoryUseCase) CreateItem(ctx context.Context, userID string, productID, variantID *string, initialQuantity int64) (*domain.InventoryItem, error) {
	if initialQuantity < 0 {
		return nil, apperror.Validation("Initial quantity cannot be negative")
	}

	vendorID, resolvedProductID, err := uc.verifyItemOwnership(ctx, userID, productID, variantID)
	if err != nil {
		return nil, err
	}

	item := &domain.InventoryItem{ProductID: resolvedProductID, VariantID: variantID, VendorID: vendorID, AvailableQuantity: initialQuantity}
	if err := uc.items.Create(ctx, item); err != nil {
		if errors.Is(err, repository.ErrItemAlreadyExists) {
			return nil, apperror.Conflict("Stock has already been set up for this product")
		}
		return nil, apperror.Internal(err)
	}
	return item, nil
}

// RequestRestock replaces what used to be an immediate stock increase: a
// vendor asking to add more units to a product that's already gone through
// the full submit-for-review → approved lifecycle now only creates a
// pending request — available_quantity is untouched until an admin approves
// it (see ApproveRestockRequest). This only applies to stock already set up
// via CreateItem; it never creates a product/variant's first-ever stock row.
func (uc *InventoryUseCase) RequestRestock(ctx context.Context, userID string, productID, variantID *string, quantity int64) (*domain.RestockRequest, error) {
	if err := domain.ValidateQuantity(quantity); err != nil {
		return nil, err
	}

	vendorID, resolvedProductID, err := uc.verifyItemOwnership(ctx, userID, productID, variantID)
	if err != nil {
		return nil, err
	}

	status, err := uc.catalog.GetProductStatus(ctx, resolvedProductID)
	if err != nil {
		return nil, err
	}
	if status != "approved" {
		return nil, apperror.Conflict("Product must be approved before requesting additional stock")
	}

	var item *domain.InventoryItem
	if variantID != nil {
		item, err = uc.items.FindByVariantID(ctx, *variantID)
	} else {
		item, err = uc.items.FindByProductID(ctx, resolvedProductID)
	}
	if err != nil {
		if errors.Is(err, repository.ErrItemNotFound) {
			return nil, apperror.NotFound("Stock has not been set up for this product yet")
		}
		return nil, apperror.Internal(err)
	}

	req := &domain.RestockRequest{
		InventoryItemID:   item.ID,
		ProductID:         resolvedProductID,
		VariantID:         variantID,
		VendorID:          vendorID,
		RequestedQuantity: quantity,
		RequestedBy:       userID,
	}
	if err := uc.restockRequests.Create(ctx, req); err != nil {
		return nil, apperror.Internal(err)
	}
	return req, nil
}

// ListMyRestockRequests lets a vendor see the status of their own restock
// requests — otherwise requesting a stock increase gives no visible
// feedback once it leaves available_quantity untouched.
func (uc *InventoryUseCase) ListMyRestockRequests(ctx context.Context, userID, vendorID string, limit, offset int) ([]*domain.RestockRequest, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}

	requests, err := uc.restockRequests.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return requests, nil
}

// ListRestockRequestsForAdmin backs the admin moderation queue for stock
// increases, mirroring ProductUseCase.ListForModeration's shape.
func (uc *InventoryUseCase) ListRestockRequestsForAdmin(ctx context.Context, status string, limit, offset int) ([]*domain.RestockRequest, error) {
	if status != "" {
		switch domain.RestockStatus(status) {
		case domain.RestockPending, domain.RestockApproved, domain.RestockRejected:
		default:
			return nil, apperror.Validation("Invalid status filter")
		}
	}

	requests, err := uc.restockRequests.ListByStatus(ctx, status, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return requests, nil
}

// ApproveRestockRequest applies the requested quantity increase (and its
// stock_movements audit row, via the same atomic Restock/RestockVariant
// path CreateItem's sibling always used) and marks the request approved.
func (uc *InventoryUseCase) ApproveRestockRequest(ctx context.Context, adminUserID, requestID string) (*domain.RestockRequest, error) {
	req, err := uc.findRestockRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if !domain.CanTransitionRestock(req.Status, domain.RestockApproved) {
		return nil, apperror.Conflict("Only a pending restock request can be approved")
	}

	if req.VariantID != nil {
		err = uc.items.RestockVariant(ctx, *req.VariantID, req.RequestedQuantity)
	} else {
		err = uc.items.Restock(ctx, req.ProductID, req.RequestedQuantity)
	}
	if err != nil {
		var notStocked *repository.ErrProductNotStocked
		if errors.As(err, &notStocked) {
			return nil, apperror.NotFound("Stock has not been set up for this product yet")
		}
		return nil, apperror.Internal(err)
	}

	if err := uc.restockRequests.UpdateStatus(ctx, req.ID, domain.RestockApproved, adminUserID, nil); err != nil {
		return nil, apperror.Internal(err)
	}
	req.Status = domain.RestockApproved
	return req, nil
}

func (uc *InventoryUseCase) RejectRestockRequest(ctx context.Context, adminUserID, requestID, reason string) (*domain.RestockRequest, error) {
	if reason == "" {
		return nil, apperror.Validation("A rejection reason is required")
	}

	req, err := uc.findRestockRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if !domain.CanTransitionRestock(req.Status, domain.RestockRejected) {
		return nil, apperror.Conflict("Only a pending restock request can be rejected")
	}

	if err := uc.restockRequests.UpdateStatus(ctx, req.ID, domain.RestockRejected, adminUserID, &reason); err != nil {
		return nil, apperror.Internal(err)
	}
	req.Status = domain.RestockRejected
	req.RejectionReason = &reason
	return req, nil
}

func (uc *InventoryUseCase) findRestockRequest(ctx context.Context, id string) (*domain.RestockRequest, error) {
	req, err := uc.restockRequests.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrRestockRequestNotFound) {
			return nil, apperror.NotFound("Restock request not found")
		}
		return nil, apperror.Internal(err)
	}
	return req, nil
}

// CheckStockReadiness backs Catalog's SubmitForReview completeness gate: a
// plain (non-variant) product needs an inventory row with a positive
// quantity, a variant product needs every listed variant to have one.
func (uc *InventoryUseCase) CheckStockReadiness(ctx context.Context, productID string, variantIDs []string) (bool, error) {
	if len(variantIDs) == 0 {
		item, err := uc.items.FindByProductID(ctx, productID)
		if err != nil {
			if errors.Is(err, repository.ErrItemNotFound) {
				return false, nil
			}
			return false, apperror.Internal(err)
		}
		return item.AvailableQuantity > 0, nil
	}

	stock, err := uc.items.ListByVariantIDs(ctx, variantIDs)
	if err != nil {
		return false, apperror.Internal(err)
	}
	for _, id := range variantIDs {
		qty, ok := stock[id]
		if !ok || qty <= 0 {
			return false, nil
		}
	}
	return true, nil
}

// GetProductStock resolves current stock for a non-variant product, for
// Catalog's admin moderation detail view. ok is false when no inventory row
// exists yet.
func (uc *InventoryUseCase) GetProductStock(ctx context.Context, productID string) (int64, bool, error) {
	item, err := uc.items.FindByProductID(ctx, productID)
	if err != nil {
		if errors.Is(err, repository.ErrItemNotFound) {
			return 0, false, nil
		}
		return 0, false, apperror.Internal(err)
	}
	return item.AvailableQuantity, true, nil
}

// GetStockForVariants serves Catalog's public product-detail page: current
// stock per variant, no ownership check (internal/trusted, same shape as
// Reserve/Release/Commit — a variant missing from the result just means no
// inventory row exists for it yet, not an error).
func (uc *InventoryUseCase) GetStockForVariants(ctx context.Context, variantIDs []string) (map[string]int64, error) {
	stock, err := uc.items.ListByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return stock, nil
}

func (uc *InventoryUseCase) ListMine(ctx context.Context, userID, vendorID string, limit, offset int) ([]*domain.InventoryItem, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}

	items, err := uc.items.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return items, nil
}

// Reserve holds stock for a checkout. It is called internally by Order and
// never by an end user directly, so it takes an already-authenticated
// order id and product lines rather than a user id.
func (uc *InventoryUseCase) Reserve(ctx context.Context, orderID string, lines []domain.ReservationLine) ([]*domain.Reservation, error) {
	if orderID == "" {
		return nil, apperror.Validation("order_id is required")
	}
	if len(lines) == 0 {
		return nil, apperror.Validation("At least one line item is required")
	}
	for _, line := range lines {
		if err := domain.ValidateQuantity(line.Quantity); err != nil {
			return nil, err
		}
	}

	reservations, err := uc.reservations.ReserveAtomic(ctx, orderID, lines)
	if err != nil {
		var notStocked *repository.ErrProductNotStocked
		if errors.As(err, &notStocked) {
			return nil, apperror.Conflict("Product " + notStocked.ProductID + " is not available for sale")
		}
		var insufficient *repository.ErrInsufficientStock
		if errors.As(err, &insufficient) {
			return nil, apperror.Conflict("Not enough stock available for product " + insufficient.ProductID)
		}
		return nil, apperror.Internal(err)
	}
	return reservations, nil
}

// Release returns any active reservation held for orderID back to available
// stock. It is idempotent so a retried cancel/payment-failure event can't
// double-release.
func (uc *InventoryUseCase) Release(ctx context.Context, orderID string) error {
	if orderID == "" {
		return apperror.Validation("order_id is required")
	}
	if err := uc.reservations.ReleaseByOrderID(ctx, orderID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// Commit finalizes orderID's held reservation into a permanent stock
// decrement once Order confirms payment succeeded. It is idempotent so a
// retried payment webhook can't double-commit.
func (uc *InventoryUseCase) Commit(ctx context.Context, orderID string) error {
	if orderID == "" {
		return apperror.Validation("order_id is required")
	}
	if err := uc.reservations.CommitByOrderID(ctx, orderID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// verifyItemOwnership resolves who owns the target of a stock operation —
// the product/variant itself already names a fixed vendor id, so there's
// nothing for the client to disambiguate here — and confirms that vendor is
// both userID's own and approved. Exactly one of productID/variantID must
// be set. For a variant-scoped call, the owning vendor and the resolved
// product id both come from GetVariantOwner — a client-supplied productID
// is never consulted in that case, closing off a vendor passing a product
// they own alongside a variant they don't.
func (uc *InventoryUseCase) verifyItemOwnership(ctx context.Context, userID string, productID, variantID *string) (vendorID, resolvedProductID string, err error) {
	if variantID != nil {
		ownerVendorID, ownerProductID, err := uc.catalog.GetVariantOwner(ctx, *variantID)
		if err != nil {
			return "", "", err
		}
		if _, err := uc.vendors.GetApprovedVendorID(ctx, userID, ownerVendorID); err != nil {
			return "", "", apperror.Forbidden("You do not own this product")
		}
		return ownerVendorID, ownerProductID, nil
	}

	if productID == nil {
		return "", "", apperror.Validation("product_id or variant_id is required")
	}
	ownerVendorID, err := uc.catalog.GetProductOwnerVendorID(ctx, *productID)
	if err != nil {
		return "", "", err
	}
	if _, err := uc.vendors.GetApprovedVendorID(ctx, userID, ownerVendorID); err != nil {
		return "", "", apperror.Forbidden("You do not own this product")
	}
	return ownerVendorID, *productID, nil
}
