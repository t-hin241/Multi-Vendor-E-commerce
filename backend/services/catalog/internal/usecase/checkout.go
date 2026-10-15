package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"

	"github.com/google/uuid"
)

// ReadCheckout batches the same fresh facts/permission checks as the legacy
// single-product lookup. The 50-ID limits match Cart's maximum line count.
func (uc *ProductUseCase) ReadCheckout(ctx context.Context, productIDs, variantIDs []string) (*domain.CheckoutSnapshot, error) {
	if len(productIDs) == 0 || len(productIDs) > 50 || len(variantIDs) > 50 {
		return nil, apperror.Validation("Checkout snapshot requires 1-50 product IDs and at most 50 variant IDs")
	}
	for _, ids := range [][]string{productIDs, variantIDs} {
		for _, id := range ids {
			if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
				return nil, apperror.Validation("Checkout snapshot IDs must be canonical UUIDs")
			}
		}
	}
	snapshot, err := uc.products.ReadCheckout(ctx, productIDs, variantIDs)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	vendorIDs := []string{}
	seen := map[string]bool{}
	for _, p := range snapshot.Products {
		if p.Status == domain.StatusApproved && p.IsActive && !seen[p.VendorID] {
			seen[p.VendorID] = true
			vendorIDs = append(vendorIDs, p.VendorID)
		}
	}
	if len(vendorIDs) == 0 {
		return snapshot, nil
	}
	_, err = uc.vendors.Approved(ctx, vendorIDs)
	if err == nil {
		return snapshot, uc.markPausedShops(ctx, snapshot, vendorIDs)
	}
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		return nil, err
	}
	// The all-approved contract does not identify the rejected shop. Only
	// on this failure path, resolve each distinct shop to preserve per-product
	// visibility/errors. Never treat an upstream outage as a selling decision.
	denied := map[string]bool{}
	for _, id := range vendorIDs {
		if _, err := uc.vendors.Approved(ctx, []string{id}); err != nil {
			if !errors.As(err, &app) || app.Code != apperror.CodeConflict {
				return nil, err
			}
			denied[id] = true
		}
	}
	approved := []string{}
	for i := range snapshot.Products {
		if denied[snapshot.Products[i].VendorID] {
			snapshot.Products[i].IsActive = false
		}
	}
	for _, id := range vendorIDs {
		if !denied[id] {
			approved = append(approved, id)
		}
	}
	return snapshot, uc.markPausedShops(ctx, snapshot, approved)
}

// markPausedShops makes products of approved shops on vacation (AF-18)
// unsellable in the snapshot, flagged so Cart can say why. An outage is an
// error: checkout never guesses a shop is open.
func (uc *ProductUseCase) markPausedShops(ctx context.Context, snapshot *domain.CheckoutSnapshot, vendorIDs []string) error {
	if uc.Availability == nil || len(vendorIDs) == 0 {
		return nil
	}
	paused, err := uc.Availability.Paused(ctx, vendorIDs)
	if err != nil {
		return err
	}
	for i := range snapshot.Products {
		if paused[snapshot.Products[i].VendorID] {
			snapshot.Products[i].IsActive = false
			snapshot.Products[i].ShopPaused = true
		}
	}
	return nil
}

// OwnerLookup is GetByIDForOwnerLookup plus whether the shop is on vacation.
type OwnerLookup struct {
	Product            *domain.Product
	HasVariants        bool
	PackageWeightGrams *int64
	ShopPaused         bool
}

// LookupForServices serves the internal product lookup (Cart, Order,
// Inventory): a product of a shop on vacation reads as not visible, with
// ShopPaused set.
func (uc *ProductUseCase) LookupForServices(ctx context.Context, productID string) (*OwnerLookup, error) {
	p, hasVariants, weight, err := uc.GetByIDForOwnerLookup(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := &OwnerLookup{Product: p, HasVariants: hasVariants, PackageWeightGrams: weight}
	if uc.Availability == nil || !p.IsPubliclyVisible() {
		return out, nil
	}
	paused, err := uc.Availability.Paused(ctx, []string{p.VendorID})
	if err != nil {
		return nil, err
	}
	if paused[p.VendorID] {
		hidden := *p
		hidden.IsActive = false
		out.Product, out.ShopPaused = &hidden, true
	}
	return out, nil
}
