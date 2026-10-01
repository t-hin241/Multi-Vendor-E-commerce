package usecase

import (
	"context"
	"errors"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

func wrap(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return err
	}
	if errors.Is(err, repository.ErrStaleVendor) {
		return apperror.Conflict("Shop changed; reload before retrying")
	}
	if errors.Is(err, repository.ErrVendorNotFound) {
		return apperror.NotFound("Shop not found")
	}
	return apperror.Internal(err)
}
func validStatus(s string) bool {
	return s == "" || s == "pending" || s == "approved" || s == "rejected" || s == "suspended"
}
func (uc *VendorUseCase) Operations(ctx context.Context, actor string) (*domain.OperationsSummary, error) {
	if err := uc.ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return nil, err
	}
	out, err := uc.vendors.Operations(ctx)
	return out, wrap(err)
}

// Replay resends a shop's latest status to Catalog and Order. It needs a
// reason and is audited; resending the same status again is harmless (the
// consumers keep the highest version).
func (uc *VendorUseCase) Replay(ctx context.Context, actor, id, reason string) error {
	why := strings.TrimSpace(reason)
	if why == "" || len(why) > 500 {
		return apperror.Validation("A reason of at most 500 characters is required")
	}
	if err := uc.ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return err
	}
	return wrap(uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		v, err := uc.vendors.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if err := uc.ops.Events.Replay(ctx, id); err != nil {
			return err
		}
		return uc.auditLogs.Create(ctx, id, actor, "event_replayed", &why, v.Version)
	}))
}
func (uc *VendorUseCase) SaleStatus(ctx context.Context, ids []string, after string) ([]*domain.Vendor, error) {
	if len(ids) > 0 {
		return uc.vendors.ListByIDs(ctx, ids)
	}
	return uc.vendors.Snapshots(ctx, after, 100)
}
func (uc *VendorUseCase) Apply(ctx context.Context, userID, name, description string) (v *domain.Vendor, err error) {
	if err = uc.ops.Actors.RequireRole(ctx, userID, "vendor"); err != nil {
		return
	}
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		var e error
		v, e = uc.apply(ctx, userID, name, description)
		if e != nil {
			return e
		}
		return uc.ops.Events.Queue(ctx, v)
	})
	return v, wrap(err)
}
func (uc *VendorUseCase) ready(ctx context.Context, v *domain.Vendor) error {
	if err := domain.ValidateApplication(v.ShopName); err != nil {
		return err
	}
	if strings.TrimSpace(v.Description) == "" || len(v.Description) > 4000 {
		return apperror.Validation("Shop description is required (maximum 4000 bytes)")
	}
	address, err := uc.ops.Addresses.FindDefaultForVendor(ctx, v.ID)
	if errors.Is(err, repository.ErrVendorAddressNotFound) {
		return apperror.Validation("A default pickup address is required before approval or resubmission")
	}
	if err != nil {
		return err
	}
	return domain.ValidateAddress(address.RecipientName, address.Phone, address.Province, address.District, address.Ward, address.StreetAddress)
}
func (uc *VendorUseCase) decide(ctx context.Context, id, actor string, to domain.Status, reason string, restore bool) (result *domain.Vendor, err error) {
	if err = uc.ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if (to != domain.StatusApproved || restore) && reason == "" {
		return nil, apperror.Validation("A decision reason is required")
	}
	if len(reason) > 1000 {
		return nil, apperror.Validation("Decision reason exceeds 1000 bytes")
	}
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		v, e := uc.vendors.FindByID(ctx, id)
		if e != nil {
			return e
		}
		if !domain.CanTransition(v.Status, to) || (to == domain.StatusApproved && (restore != (v.Status == domain.StatusSuspended))) {
			return apperror.Conflict("Invalid shop status transition")
		}
		if to == domain.StatusApproved {
			if e = uc.ready(ctx, v); e != nil {
				return e
			}
		}
		if e = uc.vendors.UpdateStatus(ctx, id, v.Version, to, actor, &reason); e != nil {
			return e
		}
		v, e = uc.vendors.FindByID(ctx, id)
		if e != nil {
			return e
		}
		action := string(to)
		if restore {
			action = "restored"
		}
		if e = uc.auditLogs.Create(ctx, id, actor, action, &reason, v.Version); e != nil {
			return e
		}
		if e = uc.ops.Events.Queue(ctx, v); e != nil {
			return e
		}
		if to == domain.StatusApproved || to == domain.StatusRejected {
			// The owner's notice commits with the decision (no loss when
			// Notification is down); it never blocks or undoes it.
			if uc.ops.Notices == nil {
				return errors.New("decision notices are not configured")
			}
			if e = uc.ops.Notices.Queue(ctx, v.ID, v.UserID, "vendor_"+string(to), v.Version); e != nil {
				return e
			}
		}
		result = v
		return nil
	})
	return result, wrap(err)
}
func (uc *VendorUseCase) Resubmit(ctx context.Context, actor, id string) (result *domain.Vendor, err error) {
	if err = uc.ops.Actors.RequireRole(ctx, actor, "vendor"); err != nil {
		return
	}
	err = uc.ops.Tx.Run(ctx, func(ctx context.Context) error {
		v, e := uc.GetOwned(ctx, actor, id)
		if e != nil {
			return e
		}
		if v.Status != domain.StatusRejected {
			return apperror.Conflict("Only rejected shops can resubmit")
		}
		if e = uc.ready(ctx, v); e != nil {
			return e
		}
		if e = uc.vendors.UpdateStatus(ctx, id, v.Version, domain.StatusPending, actor, nil); e != nil {
			return e
		}
		v, e = uc.vendors.FindByID(ctx, id)
		if e != nil {
			return e
		}
		if e = uc.auditLogs.Create(ctx, id, actor, "resubmitted", nil, v.Version); e != nil {
			return e
		}
		if e = uc.ops.Events.Queue(ctx, v); e != nil {
			return e
		}
		result = v
		return nil
	})
	return result, wrap(err)
}
