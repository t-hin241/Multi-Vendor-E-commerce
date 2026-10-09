package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// ReturnDestinationPort stores AF-05 return destinations.
type ReturnDestinationPort interface {
	Find(ctx context.Context, vendorID string) (*domain.ReturnDestination, error)
	Set(ctx context.Context, vendorID, addressID, hours, actor string) (int64, error)
	Decide(ctx context.Context, vendorID string, version int64, actor string, verify bool, reason *string) error
	TouchAddress(ctx context.Context, addressID string) (bool, error)
	UsesAddress(ctx context.Context, addressID string) (bool, error)
}

// ReturnDestinationUseCase: the owner designates where returned goods go;
// an admin verifies it; Order reads only a verified destination.
type ReturnDestinationUseCase struct {
	Destinations ReturnDestinationPort
	Addresses    VendorAddressRepositoryPort
	Vendors      VendorRepositoryPort
	Audit        AuditLogRepositoryPort
	Ops          Operations
}

func (u *ReturnDestinationUseCase) find(ctx context.Context, vendorID string) (*domain.ReturnDestination, error) {
	d, err := u.Destinations.Find(ctx, vendorID)
	if errors.Is(err, repository.ErrReturnDestinationNotFound) {
		return nil, apperror.NotFound("This shop has no return destination yet")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return d, nil
}

// Get is the owner's view.
func (u *ReturnDestinationUseCase) Get(ctx context.Context, userID, vendorID string) (*domain.ReturnDestination, error) {
	if err := u.Ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, err
	}
	if _, err := getOwnedVendor(ctx, u.Vendors, userID, vendorID); err != nil {
		return nil, err
	}
	return u.find(ctx, vendorID)
}

// Set designates one of the shop's addresses (owner only). The new
// version waits for an admin's verification.
func (u *ReturnDestinationUseCase) Set(ctx context.Context, userID, vendorID, addressID, hours string) (*domain.ReturnDestination, error) {
	hours, err := domain.ValidateReceivingHours(hours)
	if err != nil {
		return nil, err
	}
	if err := u.Ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return nil, err
	}
	vendor, err := getOwnedVendor(ctx, u.Vendors, userID, vendorID)
	if err != nil {
		return nil, err
	}
	address, err := u.Addresses.FindByID(ctx, addressID)
	if errors.Is(err, repository.ErrVendorAddressNotFound) || (err == nil && address.VendorID != vendor.ID) {
		return nil, apperror.NotFound("Address not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	err = u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		version, err := u.Destinations.Set(ctx, vendor.ID, address.ID, hours, userID)
		if err != nil {
			return err
		}
		return u.Audit.Create(ctx, vendor.ID, userID, "return_destination_set", nil, version)
	})
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return u.find(ctx, vendor.ID)
}

// AdminGet is the admin's view of a shop's destination.
func (u *ReturnDestinationUseCase) AdminGet(ctx context.Context, adminID, vendorID string) (*domain.ReturnDestination, error) {
	if err := u.Ops.Actors.RequireRole(ctx, adminID, "admin"); err != nil {
		return nil, err
	}
	return u.find(ctx, vendorID)
}

// Decide verifies (or rejects, with a reason) exactly the version the
// admin checked; a newer version is a conflict.
func (u *ReturnDestinationUseCase) Decide(ctx context.Context, adminID, vendorID string, version int64, verify bool, reason string) (*domain.ReturnDestination, error) {
	if err := u.Ops.Actors.RequireRole(ctx, adminID, "admin"); err != nil {
		return nil, err
	}
	why, err := domain.ValidateReceivingHours(reason)
	if err != nil {
		return nil, apperror.Validation("A reason is required (at most 200 characters)")
	}
	err = u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		if err := u.Destinations.Decide(ctx, vendorID, version, adminID, verify, &why); err != nil {
			return err
		}
		action := "return_destination_rejected"
		if verify {
			action = "return_destination_verified"
		}
		if err := u.Audit.Create(ctx, vendorID, adminID, action, &why, version); err != nil {
			return err
		}
		// PW-009: the owner hears the decision (once per version and decision).
		if u.Ops.Notices == nil {
			return nil
		}
		v, err := u.Vendors.FindByID(ctx, vendorID)
		if err != nil {
			return err
		}
		return u.Ops.Notices.Queue(ctx, vendorID, v.UserID, action, version)
	})
	if errors.Is(err, repository.ErrStaleVendor) {
		return nil, apperror.Conflict("The return destination changed since you loaded it; reload and check it again")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return u.find(ctx, vendorID)
}

// Verified is Order's read: the shop's destination only when an admin
// verified its current version.
func (u *ReturnDestinationUseCase) Verified(ctx context.Context, vendorID string) (*domain.ReturnDestination, error) {
	d, err := u.find(ctx, vendorID)
	if err != nil {
		return nil, err
	}
	if !d.Verified() {
		return nil, apperror.NotFound("This shop's return destination is not verified")
	}
	return d, nil
}
