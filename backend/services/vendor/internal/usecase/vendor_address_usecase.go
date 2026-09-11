package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// VendorAddressUseCase follows the same pattern as VendorUseCase.UpdateProfile:
// resolve the caller's own vendor row first, then scope every mutation to
// it — a vendor can never see or touch another vendor's addresses.
type VendorAddressUseCase struct {
	addresses VendorAddressRepositoryPort
	vendors   VendorRepositoryPort
}

func NewVendorAddressUseCase(addresses VendorAddressRepositoryPort, vendors VendorRepositoryPort) *VendorAddressUseCase {
	return &VendorAddressUseCase{addresses: addresses, vendors: vendors}
}

// Add creates a new address for the calling vendor. The very first address
// a vendor adds automatically becomes their default.
func (uc *VendorAddressUseCase) Add(ctx context.Context, userID, recipientName, phone, province, district, ward, streetAddress string) (*domain.VendorAddress, error) {
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}
	vendor, err := uc.vendors.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("No vendor application found for this account")
		}
		return nil, apperror.Internal(err)
	}

	existing, err := uc.addresses.ListForVendor(ctx, vendor.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	address := &domain.VendorAddress{
		VendorID: vendor.ID, RecipientName: recipientName, Phone: phone,
		Province: province, District: district, Ward: ward, StreetAddress: streetAddress,
		IsDefault: len(existing) == 0,
	}
	if err := uc.addresses.Create(ctx, address); err != nil {
		return nil, apperror.Internal(err)
	}
	return address, nil
}

func (uc *VendorAddressUseCase) ListMine(ctx context.Context, userID string) ([]*domain.VendorAddress, error) {
	vendor, err := uc.vendors.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("No vendor application found for this account")
		}
		return nil, apperror.Internal(err)
	}
	addresses, err := uc.addresses.ListForVendor(ctx, vendor.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return addresses, nil
}

func (uc *VendorAddressUseCase) Update(ctx context.Context, userID, addressID, recipientName, phone, province, district, ward, streetAddress string) (*domain.VendorAddress, error) {
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}
	address, err := uc.ownedByUser(ctx, userID, addressID)
	if err != nil {
		return nil, err
	}

	address.RecipientName, address.Phone = recipientName, phone
	address.Province, address.District, address.Ward, address.StreetAddress = province, district, ward, streetAddress
	if err := uc.addresses.Update(ctx, addressID, address); err != nil {
		return nil, apperror.Internal(err)
	}
	return address, nil
}

func (uc *VendorAddressUseCase) Delete(ctx context.Context, userID, addressID string) error {
	if _, err := uc.ownedByUser(ctx, userID, addressID); err != nil {
		return err
	}
	if err := uc.addresses.Delete(ctx, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorAddressUseCase) SetDefault(ctx context.Context, userID, addressID string) error {
	address, err := uc.ownedByUser(ctx, userID, addressID)
	if err != nil {
		return err
	}
	if err := uc.addresses.SetDefault(ctx, address.VendorID, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorAddressUseCase) ownedByUser(ctx context.Context, userID, addressID string) (*domain.VendorAddress, error) {
	vendor, err := uc.vendors.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("No vendor application found for this account")
		}
		return nil, apperror.Internal(err)
	}
	address, err := uc.addresses.FindByID(ctx, addressID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorAddressNotFound) {
			return nil, apperror.NotFound("Address not found")
		}
		return nil, apperror.Internal(err)
	}
	if address.VendorID != vendor.ID {
		return nil, apperror.Forbidden("You do not have access to this address")
	}
	return address, nil
}
