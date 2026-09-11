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
	items        InventoryItemRepositoryPort
	reservations ReservationRepositoryPort
	vendors      VendorGateway
	catalog      CatalogGateway
}

func NewInventoryUseCase(
	items InventoryItemRepositoryPort,
	reservations ReservationRepositoryPort,
	vendors VendorGateway,
	catalog CatalogGateway,
) *InventoryUseCase {
	return &InventoryUseCase{items: items, reservations: reservations, vendors: vendors, catalog: catalog}
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

func (uc *InventoryUseCase) Restock(ctx context.Context, userID string, productID, variantID *string, quantity int64) (*domain.InventoryItem, error) {
	if err := domain.ValidateQuantity(quantity); err != nil {
		return nil, err
	}

	_, resolvedProductID, err := uc.verifyItemOwnership(ctx, userID, productID, variantID)
	if err != nil {
		return nil, err
	}

	if variantID != nil {
		if err := uc.items.RestockVariant(ctx, *variantID, quantity); err != nil {
			var notStocked *repository.ErrProductNotStocked
			if errors.As(err, &notStocked) {
				return nil, apperror.NotFound("Stock has not been set up for this variant yet")
			}
			return nil, apperror.Internal(err)
		}
		item, err := uc.items.FindByVariantID(ctx, *variantID)
		if err != nil {
			return nil, apperror.Internal(err)
		}
		return item, nil
	}

	if err := uc.items.Restock(ctx, resolvedProductID, quantity); err != nil {
		var notStocked *repository.ErrProductNotStocked
		if errors.As(err, &notStocked) {
			return nil, apperror.NotFound("Stock has not been set up for this product yet")
		}
		return nil, apperror.Internal(err)
	}

	item, err := uc.items.FindByProductID(ctx, resolvedProductID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return item, nil
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

func (uc *InventoryUseCase) ListMine(ctx context.Context, userID string, limit, offset int) ([]*domain.InventoryItem, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
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

// verifyItemOwnership resolves who owns the target of a stock operation and
// confirms it's userID's own approved vendor account. Exactly one of
// productID/variantID must be set. For a variant-scoped call, ownership
// and the resolved product id both come from GetVariantOwner — a
// client-supplied productID is never consulted in that case, closing off
// a vendor passing a product they own alongside a variant they don't.
func (uc *InventoryUseCase) verifyItemOwnership(ctx context.Context, userID string, productID, variantID *string) (vendorID, resolvedProductID string, err error) {
	vendorID, err = uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return "", "", err
	}

	if variantID != nil {
		ownerVendorID, ownerProductID, err := uc.catalog.GetVariantOwner(ctx, *variantID)
		if err != nil {
			return "", "", err
		}
		if ownerVendorID != vendorID {
			return "", "", apperror.Forbidden("You do not own this product")
		}
		return vendorID, ownerProductID, nil
	}

	if productID == nil {
		return "", "", apperror.Validation("product_id or variant_id is required")
	}
	ownerVendorID, err := uc.catalog.GetProductOwnerVendorID(ctx, *productID)
	if err != nil {
		return "", "", err
	}
	if ownerVendorID != vendorID {
		return "", "", apperror.Forbidden("You do not own this product")
	}
	return vendorID, *productID, nil
}
