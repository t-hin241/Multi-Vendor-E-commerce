package usecase

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// AvailabilityStore is the AF-18 vacation storage.
type AvailabilityStore interface {
	FindOpen(ctx context.Context, vendorID string) (*domain.AvailabilityInterval, error)
	Create(ctx context.Context, vendorID, actor string, start time.Time, end *time.Time, message string) (*domain.AvailabilityInterval, error)
	Reschedule(ctx context.Context, id, actor string, start time.Time, end *time.Time, message string) error
	Activate(ctx context.Context, id string, saleVersion int64, at time.Time) error
	Close(ctx context.Context, id string, status domain.IntervalStatus, actor *string, resumedVersion *int64, at time.Time) error
	BumpSchedule(ctx context.Context, vendorID string, expected int64) (int64, error)
	SetAccepting(ctx context.Context, vendorID string, accepting bool) (int64, error)
	Due(ctx context.Context, limit int) ([]string, error)
	Audit(ctx context.Context, a repository.AvailabilityAudit) error
}

// ShopAccess checks the actor's shop membership (StaffUseCase).
type ShopAccess interface {
	Require(ctx context.Context, actorID, vendorID, permission string) (*domain.Membership, error)
	GetForMember(ctx context.Context, actorID, vendorID string) (*domain.Vendor, *domain.Membership, error)
}

// AvailabilityUseCase owns AF-18 shop vacations: the owner (or staff with
// shop.availability.write) schedules a pause, the scheduler applies its
// start and end, and each flip of accepting_orders bumps the shop's
// selling version and goes out on the status outbox in the same
// transaction, so Catalog and Order fence new checkouts by version.
type AvailabilityUseCase struct {
	Store   AvailabilityStore
	Vendors VendorRepositoryPort
	Access  ShopAccess
	Tx      Transactions
	Events  EventStore
	Notices NoticeQueue
	// Enabled is FEATURE_SHOP_VACATION_ENABLED. Off, no new schedule or
	// change is accepted; cancelling, resuming and the scheduler keep
	// working, so a rollback never strands a paused shop.
	Enabled bool
	Now     func() time.Time
	Log     zerolog.Logger
}

var (
	errVacationDisabled = &apperror.Error{Code: "feature_disabled", Status: http.StatusNotFound, Message: "Shop vacation is not enabled"}
	errShopNotSelling   = &apperror.Error{Code: "shop_not_selling", Status: http.StatusConflict,
		Message: "Only an approved or suspended shop can schedule a vacation"}
	errNoVacation = apperror.NotFound("The shop has no scheduled or active vacation")
)

func (uc *AvailabilityUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func availabilityErr(err error) error {
	if errors.Is(err, repository.ErrStaleAvailability) {
		return domain.ErrAvailabilityVersion
	}
	return wrap(err)
}

// Get returns the shop's vacation state to any active member.
func (uc *AvailabilityUseCase) Get(ctx context.Context, actor, vendorID string) (*domain.Availability, error) {
	v, _, err := uc.Access.GetForMember(ctx, actor, vendorID)
	if err != nil {
		return nil, err
	}
	return uc.load(ctx, v)
}

func (uc *AvailabilityUseCase) load(ctx context.Context, v *domain.Vendor) (*domain.Availability, error) {
	cur, err := uc.Store.FindOpen(ctx, v.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return &domain.Availability{VendorID: v.ID, Status: v.Status, AcceptingOrders: v.AcceptingOrders, AvailabilityVersion: v.AvailabilityVersion,
		SaleVersion: v.Version, EnforcedVersion: v.EnforcedVersion, Current: cur}, nil
}

// Public returns a public shop's vacation state (no member check).
func (uc *AvailabilityUseCase) Public(ctx context.Context, v *domain.Vendor) (*domain.Availability, error) {
	return uc.load(ctx, v)
}

// Schedule creates or changes the shop's one open vacation. A start of now
// (or slightly behind) pauses the shop in the same transaction; a later
// start is applied by the scheduler.
func (uc *AvailabilityUseCase) Schedule(ctx context.Context, actor, vendorID string, in domain.VacationInput) (*domain.Availability, error) {
	if !uc.Enabled {
		return nil, errVacationDisabled
	}
	m, err := uc.Access.Require(ctx, actor, vendorID, shopaccess.ShopAvailabilityWrite)
	if err != nil {
		return nil, err
	}
	var out *domain.Availability
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		v, err := uc.Vendors.FindByID(ctx, vendorID)
		if err != nil {
			return err
		}
		if !domain.MayScheduleVacation(v.Status) {
			return errShopNotSelling
		}
		if v.AvailabilityVersion != in.ExpectedVersion {
			return domain.ErrAvailabilityVersion
		}
		cur, err := uc.Store.FindOpen(ctx, vendorID)
		if err != nil {
			return err
		}
		now := uc.now()
		start, end, message, err := domain.NormalizeVacation(in, cur, now)
		if err != nil {
			return err
		}
		action := "vacation_scheduled"
		if cur == nil {
			if cur, err = uc.Store.Create(ctx, vendorID, actor, start, end, message); err != nil {
				return err
			}
		} else {
			action = "vacation_changed"
			if err = uc.Store.Reschedule(ctx, cur.ID, actor, start, end, message); err != nil {
				return err
			}
			cur.StartsAt, cur.EndsAt, cur.Message = start, end, message
		}
		scheduleVersion, err := uc.Store.BumpSchedule(ctx, vendorID, in.ExpectedVersion)
		if err != nil {
			return err
		}
		membershipVersion := m.Version
		if err = uc.Store.Audit(ctx, repository.AvailabilityAudit{VendorID: vendorID, IntervalID: cur.ID, Action: action, ActorUserID: &actor,
			StartsAt: start, EndsAt: end, AvailabilityVersion: scheduleVersion, SaleVersion: v.Version, MembershipVersion: &membershipVersion,
			RequestID: middleware.RequestIDFromContext(ctx)}); err != nil {
			return err
		}
		if cur.Status == domain.IntervalScheduled && !start.After(now) {
			if err = uc.pause(ctx, v, cur, &actor, now); err != nil {
				return err
			}
		}
		if v, err = uc.Vendors.FindByID(ctx, vendorID); err != nil {
			return err
		}
		out, err = uc.load(ctx, v)
		return err
	})
	if err != nil {
		return nil, availabilityErr(err)
	}
	uc.Log.Info().Str("vendor_id", vendorID).Int64("availability_version", out.AvailabilityVersion).
		Bool("accepting_orders", out.AcceptingOrders).Msg("shop_vacation_scheduled")
	return out, nil
}

// Resume cancels a scheduled vacation or ends an active one now. It works
// with the feature flag off, so an owner can always reopen.
func (uc *AvailabilityUseCase) Resume(ctx context.Context, actor, vendorID string, expectedVersion int64) (*domain.Availability, error) {
	m, err := uc.Access.Require(ctx, actor, vendorID, shopaccess.ShopAvailabilityWrite)
	if err != nil {
		return nil, err
	}
	var out *domain.Availability
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		v, err := uc.Vendors.FindByID(ctx, vendorID)
		if err != nil {
			return err
		}
		if v.AvailabilityVersion != expectedVersion {
			return domain.ErrAvailabilityVersion
		}
		cur, err := uc.Store.FindOpen(ctx, vendorID)
		if err != nil {
			return err
		}
		if cur == nil {
			return errNoVacation
		}
		now := uc.now()
		scheduleVersion, err := uc.Store.BumpSchedule(ctx, vendorID, expectedVersion)
		if err != nil {
			return err
		}
		membershipVersion := m.Version
		if cur.Status == domain.IntervalScheduled {
			if err = uc.Store.Close(ctx, cur.ID, domain.IntervalCancelled, &actor, nil, now); err != nil {
				return err
			}
			if err = uc.Store.Audit(ctx, repository.AvailabilityAudit{VendorID: vendorID, IntervalID: cur.ID, Action: "vacation_cancelled",
				ActorUserID: &actor, StartsAt: cur.StartsAt, EndsAt: cur.EndsAt, AvailabilityVersion: scheduleVersion, SaleVersion: v.Version,
				MembershipVersion: &membershipVersion, RequestID: middleware.RequestIDFromContext(ctx)}); err != nil {
				return err
			}
		} else if err = uc.resume(ctx, v, cur, &actor, &membershipVersion, now); err != nil {
			return err
		}
		if v, err = uc.Vendors.FindByID(ctx, vendorID); err != nil {
			return err
		}
		out, err = uc.load(ctx, v)
		return err
	})
	if err != nil {
		return nil, availabilityErr(err)
	}
	uc.Log.Info().Str("vendor_id", vendorID).Int64("availability_version", out.AvailabilityVersion).Msg("shop_vacation_resumed")
	return out, nil
}

// pause stops new orders: accepting_orders off, selling version bumped and
// queued for Catalog/Order, owner told. Runs in the caller's transaction
// with the shop row locked.
func (uc *AvailabilityUseCase) pause(ctx context.Context, v *domain.Vendor, cur *domain.AvailabilityInterval, actor *string, now time.Time) error {
	saleVersion, err := uc.Store.SetAccepting(ctx, v.ID, false)
	if err != nil {
		return err
	}
	if err = uc.Store.Activate(ctx, cur.ID, saleVersion, now); err != nil {
		return err
	}
	return uc.publish(ctx, v.ID, cur, "vacation_started", "shop_vacation_started", actor, nil, saleVersion)
}

func (uc *AvailabilityUseCase) resume(ctx context.Context, v *domain.Vendor, cur *domain.AvailabilityInterval, actor *string, membershipVersion *int64, now time.Time) error {
	saleVersion, err := uc.Store.SetAccepting(ctx, v.ID, true)
	if err != nil {
		return err
	}
	if err = uc.Store.Close(ctx, cur.ID, domain.IntervalEnded, actor, &saleVersion, now); err != nil {
		return err
	}
	return uc.publish(ctx, v.ID, cur, "vacation_ended", "shop_vacation_ended", actor, membershipVersion, saleVersion)
}

// publish queues the new selling status, the audit row and the owner's
// notice with the flip.
func (uc *AvailabilityUseCase) publish(ctx context.Context, vendorID string, cur *domain.AvailabilityInterval, action, notice string,
	actor *string, membershipVersion *int64, saleVersion int64) error {
	v, err := uc.Vendors.FindByID(ctx, vendorID)
	if err != nil {
		return err
	}
	if err = uc.Events.Queue(ctx, v); err != nil {
		return err
	}
	if err = uc.Store.Audit(ctx, repository.AvailabilityAudit{VendorID: vendorID, IntervalID: cur.ID, Action: action, ActorUserID: actor,
		StartsAt: cur.StartsAt, EndsAt: cur.EndsAt, AvailabilityVersion: v.AvailabilityVersion, SaleVersion: saleVersion,
		MembershipVersion: membershipVersion, RequestID: middleware.RequestIDFromContext(ctx)}); err != nil {
		return err
	}
	if uc.Notices == nil {
		return errors.New("vacation notices are not configured")
	}
	return uc.Notices.Queue(ctx, vendorID, v.UserID, notice, saleVersion)
}

// RunScheduler applies due starts and ends every few seconds. All state is
// in PostgreSQL, so a restart (or a second replica) simply picks up what
// is due; the shop row lock makes each transition happen once.
func (uc *AvailabilityUseCase) RunScheduler(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		due, err := uc.Store.Due(ctx, 50)
		if err != nil {
			uc.Log.Error().Msg("shop_vacation_due_query_failed")
			continue
		}
		for _, vendorID := range due {
			if err := uc.Advance(ctx, vendorID); err != nil {
				uc.Log.Error().Err(err).Str("vendor_id", vendorID).Msg("shop_vacation_transition_failed")
			}
		}
	}
}

// Advance applies the shop's due transition, if any (idempotent).
func (uc *AvailabilityUseCase) Advance(ctx context.Context, vendorID string) error {
	var applied string
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		v, err := uc.Vendors.FindByID(ctx, vendorID)
		if err != nil {
			return err
		}
		cur, err := uc.Store.FindOpen(ctx, vendorID)
		if err != nil || cur == nil {
			return err
		}
		now := uc.now()
		switch {
		case cur.Status == domain.IntervalScheduled && !cur.StartsAt.After(now):
			if _, err = uc.Store.BumpSchedule(ctx, vendorID, -1); err != nil {
				return err
			}
			applied = "started"
			return uc.pause(ctx, v, cur, nil, now)
		case cur.Status == domain.IntervalActive && cur.EndsAt != nil && !cur.EndsAt.After(now):
			if _, err = uc.Store.BumpSchedule(ctx, vendorID, -1); err != nil {
				return err
			}
			applied = "ended"
			return uc.resume(ctx, v, cur, nil, nil, now)
		}
		return nil
	})
	if err == nil && applied != "" {
		uc.Log.Info().Str("vendor_id", vendorID).Str("transition", applied).Msg("shop_vacation_transition")
	}
	return err
}
