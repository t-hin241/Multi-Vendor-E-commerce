package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

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
	PendingCarrierChecks(ctx context.Context, limit int) ([]*domain.ReturnDestination, error)
	RecordCarrierCheck(ctx context.Context, vendorID string, check domain.CarrierAddressCheck, decide bool, rejection *string) error
}

// ErrAddressCheckUnsupported: the carrier offers no address check; an
// admin verifies the destination.
var ErrAddressCheckUnsupported = errors.New("carrier address check not supported")

// AddressChecker asks the carrier, through Shipment, whether it can serve
// an address (PW-042). Any other error is transient: the check is retried.
type AddressChecker interface {
	CheckAddress(ctx context.Context, a domain.VendorAddress) (deliverable bool, reason, reference string, err error)
}

// ReturnDestinationUseCase: the owner designates where returned goods go;
// an admin, or the carrier's address check, verifies it; Order reads only
// a verified destination.
type ReturnDestinationUseCase struct {
	Destinations ReturnDestinationPort
	Addresses    VendorAddressRepositoryPort
	Vendors      VendorRepositoryPort
	Audit        AuditLogRepositoryPort
	Ops          Operations
	// Carrier (PW-042, FEATURE_RETURN_DESTINATION_CARRIER_CHECK_ENABLED)
	// checks each new version; nil leaves every version to an admin.
	Carrier AddressChecker
	Log     zerolog.Logger
}

// CheckWithCarrier sends destinations waiting for a decision to the
// carrier, one version each. Deliverable verifies the version, an
// undeliverable answer rejects it with the carrier's reason (the owner is
// told either way); no address check at the carrier leaves it to an admin.
// A decision an admin made meanwhile is kept.
func (u *ReturnDestinationUseCase) CheckWithCarrier(ctx context.Context, limit int) (int, error) {
	if u.Carrier == nil {
		return 0, nil
	}
	pending, err := u.Destinations.PendingCarrierChecks(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	var errs []error
	for _, d := range pending {
		deliverable, reason, ref, err := u.Carrier.CheckAddress(ctx, *d.Address)
		check := domain.CarrierAddressCheck{Version: d.Version, Result: domain.CarrierDeliverable}
		switch {
		case errors.Is(err, ErrAddressCheckUnsupported):
			check.Result = domain.CarrierUnsupported
		case err != nil:
			errs = append(errs, err)
			continue
		case !deliverable:
			check.Result = domain.CarrierUndeliverable
		}
		reason, ref = clip(reason, 300), clip(ref, 200)
		if reason != "" {
			check.Reason = &reason
		}
		if ref != "" {
			check.Reference = &ref
		}
		if err := u.applyCarrierCheck(ctx, d.VendorID, check); err != nil && !errors.Is(err, repository.ErrStaleVendor) {
			errs = append(errs, err)
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

func (u *ReturnDestinationUseCase) applyCarrierCheck(ctx context.Context, vendorID string, check domain.CarrierAddressCheck) error {
	return u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		d, err := u.Destinations.Find(ctx, vendorID)
		if err != nil {
			return err
		}
		if d.Version != check.Version {
			return repository.ErrStaleVendor // changed meanwhile: the new version is checked next
		}
		decide := d.AwaitsDecision() && check.Result != domain.CarrierUnsupported
		var rejection *string
		if check.Result == domain.CarrierUndeliverable {
			why := "Carrier address check: the carrier cannot serve this address"
			if check.Reason != nil {
				why = "Carrier address check: " + *check.Reason
			}
			rejection = &why
		}
		if err := u.Destinations.RecordCarrierCheck(ctx, vendorID, check, decide, rejection); err != nil {
			return err
		}
		if !decide {
			return nil
		}
		action, notice, reason := "return_destination_carrier_verified", "return_destination_verified", check.Reference
		if check.Result == domain.CarrierUndeliverable {
			action, notice, reason = "return_destination_carrier_rejected", "return_destination_rejected", rejection
		}
		// The owner submitted the version the carrier decided.
		if err := u.Audit.Create(ctx, vendorID, d.UpdatedBy, action, reason, check.Version); err != nil {
			return err
		}
		if u.Ops.Notices == nil {
			return nil
		}
		v, err := u.Vendors.FindByID(ctx, vendorID)
		if err != nil {
			return err
		}
		return u.Ops.Notices.Queue(ctx, vendorID, v.UserID, notice, check.Version)
	})
}

// RunCarrierChecks checks pending destinations every 30 seconds.
func (u *ReturnDestinationUseCase) RunCarrierChecks(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		if n, err := u.CheckWithCarrier(ctx, 20); err != nil && ctx.Err() == nil {
			u.Log.Warn().Err(err).Int("checked", n).Msg("return_destination_carrier_check_failed")
		} else if n > 0 {
			u.Log.Info().Int("checked", n).Msg("return_destination_carrier_checked")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
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

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
