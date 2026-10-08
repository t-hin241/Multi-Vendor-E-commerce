package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// HoldStore persists the settlement hold ledger.
type HoldStore interface {
	Find(ctx context.Context, id string) (*domain.SettlementHold, error)
	FindBySource(ctx context.Context, sourceType, sourceID, vendorOrderID string) (*domain.SettlementHold, error)
	PayoutClaimed(ctx context.Context, vendorOrderID string) (bool, error)
	Insert(ctx context.Context, h *domain.SettlementHold) error
	Release(ctx context.Context, h *domain.SettlementHold) error
	List(ctx context.Context, vendorOrderID *string, activeOnly bool, limit, offset int) ([]*domain.SettlementHold, error)
}

// VendorLocker takes the per-vendor payout lock (PayoutRepository).
type VendorLocker interface {
	LockVendor(ctx context.Context, vendorID string) error
}

// SettlementHoldUseCase runs the hold contract of 00 §6.1. Acquire takes
// the vendor's payout lock, the same lock payout batch creation takes
// before it reads active holds, so a hold and a claim are ordered: either
// the hold is seen by the batch, or the hold sees the claim
// (payout_already_claimed). Holds have no TTL; only the source releases.
type SettlementHoldUseCase struct {
	Store   HoldStore
	Vendors VendorLocker
	Tx      Transactor
	Now     func() time.Time
	Log     zerolog.Logger
}

// now is truncated to what PostgreSQL stores, so a receipt read back
// equals the one returned when it was written.
func (uc *SettlementHoldUseCase) now() time.Time {
	now := time.Now
	if uc.Now != nil {
		now = uc.Now
	}
	return now().UTC().Truncate(time.Microsecond)
}

func holdAppError(err error) error {
	if errors.Is(err, repository.ErrHoldNotFound) {
		return apperror.NotFound("Settlement hold not found")
	}
	if errors.Is(err, repository.ErrStaleState) {
		return apperror.Conflict("The hold changed meanwhile; retry")
	}
	return asAppError(err)
}

// Acquire records a hold once per hold id. created is false for a replay.
// A hold acquired after a payout already claimed the vendor order's
// credits is still recorded (it protects what is not paid yet) and comes
// back with PayoutClaimed set: the caller must answer
// payout_already_claimed, never "protected". A hold released before its
// acquire arrived stays released.
func (uc *SettlementHoldUseCase) Acquire(ctx context.Context, in domain.HoldRequest) (*domain.SettlementHold, bool, error) {
	if err := domain.ValidateHoldRequest(in); err != nil {
		return nil, false, err
	}
	var hold *domain.SettlementHold
	created := false
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Vendors.LockVendor(ctx, in.VendorID); err != nil {
			return err
		}
		existing, err := uc.Store.Find(ctx, in.HoldID)
		switch {
		case err == nil:
			// A tombstone (released before acquire) has no source: keep it.
			if existing.Status == domain.HoldReleased && existing.SourceID == nil {
				hold = existing
				return nil
			}
			if !existing.Same(in) {
				return domain.ErrHoldConflict
			}
			hold = existing
			return nil
		case !errors.Is(err, repository.ErrHoldNotFound):
			return err
		}
		if _, err := uc.Store.FindBySource(ctx, in.SourceType, in.SourceID, in.VendorOrderID); err == nil {
			return domain.ErrHoldConflict
		} else if !errors.Is(err, repository.ErrHoldNotFound) {
			return err
		}
		claimed, err := uc.Store.PayoutClaimed(ctx, in.VendorOrderID)
		if err != nil {
			return err
		}
		now := uc.now()
		hold = &domain.SettlementHold{ID: in.HoldID, VendorID: &in.VendorID, VendorOrderID: &in.VendorOrderID, SourceType: &in.SourceType,
			SourceID: &in.SourceID, SourceVersion: &in.SourceVersion, ReasonCode: &in.ReasonCode, Status: domain.HoldActive,
			PayoutClaimed: claimed, AcquiredAt: &now}
		if err := uc.Store.Insert(ctx, hold); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, holdAppError(err)
	}
	if created {
		event := uc.Log.Info()
		if hold.PayoutClaimed {
			event = uc.Log.Warn()
		}
		event.Str("hold_id", hold.ID).Str("vendor_order_id", in.VendorOrderID).Str("source_type", in.SourceType).Str("source_id", in.SourceID).
			Bool("payout_claimed", hold.PayoutClaimed).Msg("settlement_hold_acquired")
	}
	return hold, created, nil
}

func (uc *SettlementHoldUseCase) Get(ctx context.Context, id string) (*domain.SettlementHold, error) {
	h, err := uc.Store.Find(ctx, id)
	if err != nil {
		return nil, holdAppError(err)
	}
	return h, nil
}

// Release releases a hold. Repeating it returns the same receipt; a
// release whose acquire has not arrived yet leaves a released tombstone.
func (uc *SettlementHoldUseCase) Release(ctx context.Context, id string, in domain.HoldRelease) (*domain.SettlementHold, error) {
	in, err := domain.ValidateHoldRelease(in)
	if err != nil {
		return nil, err
	}
	var hold *domain.SettlementHold
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		now := uc.now()
		h, err := uc.Store.Find(ctx, id)
		if errors.Is(err, repository.ErrHoldNotFound) {
			hold = &domain.SettlementHold{ID: id, Status: domain.HoldReleased, ReleaseOperationID: &in.OperationID,
				ReleaseSourceVersion: &in.SourceVersion, ResolutionRef: optional(in.ResolutionRef), ReleaseReason: &in.Reason, ReleasedAt: &now}
			uc.Log.Warn().Str("hold_id", id).Msg("settlement_hold_released_before_acquire")
			return uc.Store.Insert(ctx, hold)
		}
		if err != nil {
			return err
		}
		hold = h
		if h.Status == domain.HoldReleased {
			return nil
		}
		// Release under the vendor lock too, so a batch being built sees
		// either the active hold or none.
		if err := uc.Vendors.LockVendor(ctx, *h.VendorID); err != nil {
			return err
		}
		h.Status, h.ReleaseOperationID, h.ReleaseSourceVersion, h.ResolutionRef, h.ReleaseReason, h.ReleasedAt =
			domain.HoldReleased, &in.OperationID, &in.SourceVersion, optional(in.ResolutionRef), &in.Reason, &now
		return uc.Store.Release(ctx, h)
	})
	if err != nil {
		return nil, holdAppError(err)
	}
	uc.Log.Info().Str("hold_id", id).Str("operation_id", in.OperationID).Msg("settlement_hold_released")
	return hold, nil
}

// List is the finance view of holds (active only by default).
func (uc *SettlementHoldUseCase) List(ctx context.Context, vendorOrderID *string, activeOnly bool, limit, offset int) ([]*domain.SettlementHold, error) {
	out, err := uc.Store.List(ctx, vendorOrderID, activeOnly, limit, offset)
	return out, holdAppError(err)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
