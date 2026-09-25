package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// VendorShippingMethodUseCase follows the same "resolve the caller's own
// vendor id first, then scope every mutation to it" pattern as Vendor's
// own UpdateProfile.
type VendorShippingMethodUseCase struct {
	methods  VendorShippingMethodRepositoryPort
	carriers CarrierRepositoryPort
	vendors  VendorGateway
}

func NewVendorShippingMethodUseCase(methods VendorShippingMethodRepositoryPort, carriers CarrierRepositoryPort, vendors VendorGateway) *VendorShippingMethodUseCase {
	return &VendorShippingMethodUseCase{methods: methods, carriers: carriers, vendors: vendors}
}

// Enable turns on one of the admin's active carriers for the named shop —
// a user may own several (1:N), so vendorID is always given explicitly and
// confirmed to be the caller's own. The very first method a shop enables
// automatically becomes its default — otherwise checkout would have
// nothing to auto-select until the vendor remembers to set one explicitly.
func (uc *VendorShippingMethodUseCase) Enable(ctx context.Context, userID, vendorID, carrierID string) (*domain.VendorShippingMethod, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	carrier, err := uc.carriers.FindByID(ctx, carrierID)
	if err != nil || !carrier.IsActive {
		return nil, apperror.Validation("Unknown or inactive carrier")
	}

	existing, err := uc.methods.ListForVendor(ctx, vendorID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	method := &domain.VendorShippingMethod{VendorID: vendorID, CarrierID: carrierID, IsDefault: len(existing) == 0}
	if err := uc.methods.Create(ctx, method); err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodAlreadyExists) {
			return nil, apperror.Conflict("This carrier is already enabled for your shop")
		}
		return nil, apperror.Internal(err)
	}
	return method, nil
}

func (uc *VendorShippingMethodUseCase) ListMine(ctx context.Context, userID, vendorID string) ([]*domain.VendorShippingMethod, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	methods, err := uc.methods.ListForVendor(ctx, vendorID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return methods, nil
}

func (uc *VendorShippingMethodUseCase) SetDefault(ctx context.Context, userID, methodID string) error {
	method, err := uc.ownedByUser(ctx, userID, methodID)
	if err != nil {
		return err
	}
	if err := uc.methods.SetDefault(ctx, method.VendorID, methodID); err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return apperror.NotFound("Shipping method not found")
		}
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorShippingMethodUseCase) SetActive(ctx context.Context, userID, methodID string, isActive bool) error {
	method, err := uc.ownedByUser(ctx, userID, methodID)
	if err != nil {
		return err
	}
	if err := uc.methods.SetActive(ctx, method.VendorID, methodID, isActive); err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return apperror.NotFound("Shipping method not found")
		}
		return apperror.Internal(err)
	}
	return nil
}

// ownedByUser derives the owning shop from the method itself — it already
// has a fixed vendor_id — rather than asking the client which shop it
// means, and confirms userID actually owns that shop.
func (uc *VendorShippingMethodUseCase) ownedByUser(ctx context.Context, userID, methodID string) (*domain.VendorShippingMethod, error) {
	method, err := uc.methods.FindByID(ctx, methodID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return nil, apperror.NotFound("Shipping method not found")
		}
		return nil, apperror.Internal(err)
	}
	if _, err := uc.vendors.GetApprovedVendorID(ctx, userID, method.VendorID); err != nil {
		return nil, apperror.Forbidden("You do not have access to this shipping method")
	}
	return method, nil
}
