package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// Buyer address book methods on OrderUseCase — mirrors Vendor's own
// warehouse-address pattern, kept on the same big OrderUseCase struct
// rather than a separate type, consistent with how every other
// buyer/vendor/admin concern in this service already lives on it.

func (uc *OrderUseCase) AddAddress(ctx context.Context, buyerID, recipientName, phone, province, district, ward, streetAddress string) (*domain.BuyerAddress, error) {
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}

	existing, err := uc.buyerAddresses.ListForBuyer(ctx, buyerID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	address := &domain.BuyerAddress{
		BuyerID: buyerID, RecipientName: recipientName, Phone: phone,
		Province: province, District: district, Ward: ward, StreetAddress: streetAddress,
		IsDefault: len(existing) == 0,
	}
	if err := uc.buyerAddresses.Create(ctx, address); err != nil {
		return nil, apperror.Internal(err)
	}
	return address, nil
}

func (uc *OrderUseCase) ListMyAddresses(ctx context.Context, buyerID string) ([]*domain.BuyerAddress, error) {
	addresses, err := uc.buyerAddresses.ListForBuyer(ctx, buyerID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return addresses, nil
}

func (uc *OrderUseCase) UpdateAddress(ctx context.Context, buyerID, addressID, recipientName, phone, province, district, ward, streetAddress string) (*domain.BuyerAddress, error) {
	if err := domain.ValidateAddress(recipientName, phone, province, district, ward, streetAddress); err != nil {
		return nil, err
	}
	address, err := uc.ownedAddress(ctx, buyerID, addressID)
	if err != nil {
		return nil, err
	}

	address.RecipientName, address.Phone = recipientName, phone
	address.Province, address.District, address.Ward, address.StreetAddress = province, district, ward, streetAddress
	if err := uc.buyerAddresses.Update(ctx, addressID, address); err != nil {
		return nil, apperror.Internal(err)
	}
	return address, nil
}

func (uc *OrderUseCase) DeleteAddress(ctx context.Context, buyerID, addressID string) error {
	if _, err := uc.ownedAddress(ctx, buyerID, addressID); err != nil {
		return err
	}
	if err := uc.buyerAddresses.Delete(ctx, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *OrderUseCase) SetDefaultAddress(ctx context.Context, buyerID, addressID string) error {
	if _, err := uc.ownedAddress(ctx, buyerID, addressID); err != nil {
		return err
	}
	if err := uc.buyerAddresses.SetDefault(ctx, buyerID, addressID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *OrderUseCase) ownedAddress(ctx context.Context, buyerID, addressID string) (*domain.BuyerAddress, error) {
	address, err := uc.buyerAddresses.FindByID(ctx, addressID)
	if err != nil {
		if errors.Is(err, repository.ErrBuyerAddressNotFound) {
			return nil, apperror.NotFound("Address not found")
		}
		return nil, apperror.Internal(err)
	}
	if address.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this address")
	}
	return address, nil
}
