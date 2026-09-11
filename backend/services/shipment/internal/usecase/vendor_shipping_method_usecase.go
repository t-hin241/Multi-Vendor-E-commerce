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

// Enable turns on one of the admin's active carriers for the calling
// vendor's own shop. The very first method a vendor enables automatically
// becomes their default — otherwise checkout would have nothing to
// auto-select until the vendor remembers to set one explicitly.
func (uc *VendorShippingMethodUseCase) Enable(ctx context.Context, userID, carrierID string) (*domain.VendorShippingMethod, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
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

func (uc *VendorShippingMethodUseCase) ListMine(ctx context.Context, userID string) ([]*domain.VendorShippingMethod, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
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
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return err
	}
	if err := uc.ownedByVendor(ctx, vendorID, methodID); err != nil {
		return err
	}
	if err := uc.methods.SetDefault(ctx, vendorID, methodID); err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return apperror.NotFound("Shipping method not found")
		}
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorShippingMethodUseCase) SetActive(ctx context.Context, userID, methodID string, isActive bool) error {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID)
	if err != nil {
		return err
	}
	if err := uc.ownedByVendor(ctx, vendorID, methodID); err != nil {
		return err
	}
	if err := uc.methods.SetActive(ctx, vendorID, methodID, isActive); err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return apperror.NotFound("Shipping method not found")
		}
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorShippingMethodUseCase) ownedByVendor(ctx context.Context, vendorID, methodID string) error {
	method, err := uc.methods.FindByID(ctx, methodID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorShippingMethodNotFound) {
			return apperror.NotFound("Shipping method not found")
		}
		return apperror.Internal(err)
	}
	if method.VendorID != vendorID {
		return apperror.Forbidden("You do not have access to this shipping method")
	}
	return nil
}
