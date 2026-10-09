package usecase

import (
	"context"
	"errors"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// VendorAddressUseCase follows the same pattern as VendorUseCase.UpdateProfile:
// resolve the caller's ownership of the named shop first (VendorUseCase.GetOwned),
// then scope every mutation to it — a vendor can never see or touch another
// shop's addresses, including another shop of their own.
type VendorAddressUseCase struct {
	addresses VendorAddressRepositoryPort
	vendors   VendorRepositoryPort
	ops       Operations
	// Destinations (AF-05), when set: editing the return destination's
	// address makes it unverified; deleting it is refused.
	Destinations ReturnDestinationPort
}

func NewVendorAddressUseCase(addresses VendorAddressRepositoryPort, vendors VendorRepositoryPort, ops Operations) *VendorAddressUseCase {
	return &VendorAddressUseCase{addresses: addresses, vendors: vendors, ops: ops}
}

// Add creates a new address for the given shop. The very first address a
// shop adds automatically becomes its default.
func (uc *VendorAddressUseCase) add(ctx context.Context, userID, vendorID, recipientName, phone, province, district, ward, streetAddress string) (*domain.VendorAddress, error) {
	recipientName, phone, province, district, ward, streetAddress = strings.TrimSpace(recipientName), strings.TrimSpace(phone), strings.TrimSpace(province), strings.TrimSpace(district), strings.TrimSpace(ward), strings.TrimSpace(streetAddress)
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}
	vendor, err := getOwnedVendor(ctx, uc.vendors, userID, vendorID)
	if err != nil {
		return nil, err
	}

	existing, err := uc.addresses.ListForVendor(ctx, vendor.ID, 1, 0)
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

func (uc *VendorAddressUseCase) ListMine(ctx context.Context, userID, vendorID string, limit, offset int) ([]*domain.VendorAddress, error) {
	if err := uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, err
	}
	vendor, err := getOwnedVendor(ctx, uc.vendors, userID, vendorID)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid pagination")
	}
	addresses, err := uc.addresses.ListForVendor(ctx, vendor.ID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return addresses, nil
}

func (uc *VendorAddressUseCase) update(ctx context.Context, userID, vendorID, addressID, recipientName, phone, province, district, ward, streetAddress string) (*domain.VendorAddress, error) {
	recipientName, phone, province, district, ward, streetAddress = strings.TrimSpace(recipientName), strings.TrimSpace(phone), strings.TrimSpace(province), strings.TrimSpace(district), strings.TrimSpace(ward), strings.TrimSpace(streetAddress)
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}
	address, err := uc.ownedByUser(ctx, userID, vendorID, addressID)
	if err != nil {
		return nil, err
	}

	address.RecipientName, address.Phone = recipientName, phone
	address.Province, address.District, address.Ward, address.StreetAddress = province, district, ward, streetAddress
	if err := uc.addresses.Update(ctx, addressID, address); err != nil {
		return nil, apperror.Internal(err)
	}
	if uc.Destinations != nil {
		if _, err := uc.Destinations.TouchAddress(ctx, addressID); err != nil {
			return nil, apperror.Internal(err)
		}
	}
	return address, nil
}

func (uc *VendorAddressUseCase) delete(ctx context.Context, userID, vendorID, addressID string) error {
	address, err := uc.ownedByUser(ctx, userID, vendorID, addressID)
	if err != nil {
		return err
	}
	if address.IsDefault {
		return apperror.Conflict("Choose another default pickup address before deleting this address")
	}
	if uc.Destinations != nil {
		used, err := uc.Destinations.UsesAddress(ctx, addressID)
		if err != nil {
			return apperror.Internal(err)
		}
		if used {
			return apperror.Conflict("This address receives returned goods; choose another return destination first")
		}
	}
	if err := uc.addresses.Delete(ctx, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorAddressUseCase) setDefault(ctx context.Context, userID, vendorID, addressID string) error {
	address, err := uc.ownedByUser(ctx, userID, vendorID, addressID)
	if err != nil {
		return err
	}
	if err := uc.addresses.SetDefault(ctx, address.VendorID, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *VendorAddressUseCase) ownedByUser(ctx context.Context, userID, vendorID, addressID string) (*domain.VendorAddress, error) {
	vendor, err := getOwnedVendor(ctx, uc.vendors, userID, vendorID)
	if err != nil {
		return nil, err
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

func (uc *VendorAddressUseCase) Add(ctx context.Context, userID, vendorID, name, phone, province, district, ward, street string) (out *domain.VendorAddress, err error) {
	if err = uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return
	}
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		var e error
		out, e = uc.add(ctx, userID, vendorID, name, phone, province, district, ward, street)
		return e
	})
	return out, wrap(err)
}
func (uc *VendorAddressUseCase) Update(ctx context.Context, userID, vendorID, id, name, phone, province, district, ward, street string) (out *domain.VendorAddress, err error) {
	if err = uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return
	}
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		var e error
		out, e = uc.update(ctx, userID, vendorID, id, name, phone, province, district, ward, street)
		return e
	})
	return out, wrap(err)
}
func (uc *VendorAddressUseCase) Delete(ctx context.Context, userID, vendorID, id string) error {
	if err := uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return err
	}
	return wrap(uc.ops.Tx.Run(ctx, func(ctx context.Context) error { return uc.delete(ctx, userID, vendorID, id) }))
}
func (uc *VendorAddressUseCase) SetDefault(ctx context.Context, userID, vendorID, id string) error {
	if err := uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return err
	}
	return wrap(uc.ops.Tx.Run(ctx, func(ctx context.Context) error { return uc.setDefault(ctx, userID, vendorID, id) }))
}
