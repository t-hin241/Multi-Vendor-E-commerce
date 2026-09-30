package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
)

// RecordStockCount lets a vendor record a physical count of their own
// stock item. It can only lower available stock (lost or damaged units);
// reserved units held for pending orders are never changed, and adding
// units still requires an approved restock request. countID is chosen by
// the client so a retry after a lost response is not applied twice.
func (uc *InventoryUseCase) RecordStockCount(ctx context.Context, userID, itemID, countID string, countedOnHand int64, reason string) (*domain.StockCount, bool, error) {
	reason, err := domain.ValidateStockCountInput(countedOnHand, reason)
	if err != nil {
		return nil, false, err
	}
	item, err := uc.items.FindByID(ctx, itemID)
	if err != nil {
		if errors.Is(err, repository.ErrItemNotFound) {
			return nil, false, apperror.NotFound("Stock item not found")
		}
		return nil, false, inventoryError(err)
	}
	// Ownership comes from the stored item, never from the request.
	if _, err := uc.vendors.GetApprovedVendorID(ctx, userID, item.VendorID); err != nil {
		return nil, false, err
	}

	count, replayed, err := uc.items.RecordStockCount(ctx, &domain.StockCount{
		ID: countID, InventoryItemID: item.ID, CountedOnHand: countedOnHand, ActorUserID: userID, Reason: reason,
	})
	if err != nil {
		if errors.Is(err, repository.ErrItemNotFound) {
			return nil, false, apperror.NotFound("Stock item not found")
		}
		return nil, false, inventoryError(err)
	}
	return count, replayed, nil
}
