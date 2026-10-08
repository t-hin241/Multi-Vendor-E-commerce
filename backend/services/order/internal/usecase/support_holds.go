package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// SupportHoldPort stores support cases' settlement holds (PW-001).
type SupportHoldPort interface {
	Get(ctx context.Context, caseID string) (*domain.CaseHold, error)
	Prepare(ctx context.Context, caseID, holdID string) (bool, error)
	SetStatus(ctx context.Context, caseID string, from []string, to string, note *string) (bool, error)
	ListMissing(ctx context.Context, limit int) ([]repository.CaseRef, error)
	ListUnreleased(ctx context.Context, limit int) ([]repository.CaseRef, error)
	Counts(ctx context.Context) (preparing, needsReview, releasing int64, err error)
}

// SettlementHoldGateway is Payment's hold ledger (00 §6.1).
type SettlementHoldGateway interface {
	AcquireSettlementHold(ctx context.Context, r adapter.HoldRequest) (*adapter.HoldReceipt, error)
	ReleaseSettlementHold(ctx context.Context, holdID string, r adapter.HoldRelease) (*adapter.HoldReceipt, error)
}

// errHoldsNotWired keeps a hold effect retrying instead of dropping it.
var errHoldsNotWired = errors.New("settlement hold ledger is not wired")

// holdLedger: new holds are acquired only with the ledger on; holds that
// exist are always released (also after the flag is turned off).
func (uc *OrderUseCase) holdLedger() bool {
	return uc.SupportConfig.HoldLedger && uc.CaseHolds != nil && uc.Holds != nil
}

// prepareCaseHold gives a case that may affect money a hold in state
// preparing and queues its acquire, in the case's transaction.
func (uc *OrderUseCase) prepareCaseHold(ctx context.Context, c *domain.SupportCase) error {
	if !uc.holdLedger() || !c.FinancialHold {
		return nil
	}
	prepared, err := uc.CaseHolds.Prepare(ctx, c.ID, uuid.NewString())
	if err != nil || !prepared {
		return err
	}
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: c.OrderID, Kind: domain.EffectAcquireSettlementHold, Target: c.ID})
}

// releaseCaseHoldOnClose queues the release of a closing case's hold, in
// the close's transaction.
func (uc *OrderUseCase) releaseCaseHoldOnClose(ctx context.Context, c *domain.SupportCase) error {
	if uc.CaseHolds == nil || uc.Holds == nil {
		return nil
	}
	hold, err := uc.CaseHolds.Get(ctx, c.ID)
	if err != nil || hold == nil || !hold.Open() {
		return err
	}
	if _, err := uc.CaseHolds.SetStatus(ctx, c.ID, []string{domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview}, domain.HoldReleasing, nil); err != nil {
		return err
	}
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: c.OrderID, Kind: domain.EffectReleaseSettlementHold, Target: c.ID})
}

// requireCaseHold: a refund or return resolution changes what the vendor
// is owed, so the case's hold must be confirmed by Payment first (a hold
// Payment flagged for review still counts: the buyer's refund is owed
// whatever happened to the payout).
func (uc *OrderUseCase) requireCaseHold(ctx context.Context, c *domain.SupportCase) error {
	if !uc.holdLedger() || !c.FinancialHold {
		return nil
	}
	hold, err := uc.CaseHolds.Get(ctx, c.ID)
	if err != nil {
		return err
	}
	if hold == nil || hold.Status == domain.HoldPreparing {
		return domain.HoldUnavailable()
	}
	return nil
}

// CaseHold is the case's hold for the admin view (nil: none).
func (uc *OrderUseCase) CaseHold(ctx context.Context, caseID string) (*domain.CaseHold, error) {
	if uc.CaseHolds == nil {
		return nil, nil
	}
	h, err := uc.CaseHolds.Get(ctx, caseID)
	if err != nil {
		return nil, appError(err)
	}
	return h, nil
}

// acquireCaseHold is the acquire effect. The answer is recorded under the
// order lock and only moves a hold still preparing (a case closed
// meanwhile is released by its own effect).
func (uc *OrderUseCase) acquireCaseHold(ctx context.Context, e *domain.Effect) error {
	if uc.CaseHolds == nil || uc.Holds == nil {
		return errHoldsNotWired
	}
	c, err := uc.Support.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	hold, err := uc.CaseHolds.Get(ctx, c.ID)
	if err != nil || hold == nil || hold.Status != domain.HoldPreparing {
		return asError(err)
	}
	receipt, err := uc.Holds.AcquireSettlementHold(ctx, adapter.HoldRequest{HoldID: hold.HoldID, VendorID: c.VendorID, VendorOrderID: c.VendorOrderID,
		SourceType: "support_case", SourceID: c.ID, SourceVersion: c.Version, ReasonCode: domain.HoldReasonCode(c)})
	to, note := domain.HoldActive, (*string)(nil)
	switch {
	case err == nil && receipt.Status == "released":
		to = domain.HoldReleased
	case err == nil:
	case isPermanent(err):
		// payout_already_claimed, or a refusal nobody can retry away.
		message := appError(err).Message
		to, note = domain.HoldNeedsReview, &message
	default:
		return err
	}
	err = uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		moved, err := uc.CaseHolds.SetStatus(ctx, c.ID, []string{domain.HoldPreparing}, to, note)
		if err != nil || !moved {
			return err
		}
		return uc.Support.AddEvent(ctx, &domain.SupportCaseEvent{CaseID: c.ID, ActorRole: "system", Action: "settlement_hold_" + to,
			ToStatus: string(c.Status), Note: note})
	})
	if err != nil {
		return err
	}
	event := uc.Log.Info()
	if to == domain.HoldNeedsReview {
		event = uc.Log.Warn()
	}
	event.Str("case_id", c.ID).Str("order_id", c.OrderID).Str("vendor_order_id", c.VendorOrderID).Str("hold_status", to).Msg("order_support_hold_recorded")
	return nil
}

// releaseCaseHold is the release effect.
func (uc *OrderUseCase) releaseCaseHold(ctx context.Context, e *domain.Effect) error {
	if uc.CaseHolds == nil || uc.Holds == nil {
		return errHoldsNotWired
	}
	c, err := uc.Support.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	hold, err := uc.CaseHolds.Get(ctx, c.ID)
	if err != nil || hold == nil || hold.Status != domain.HoldReleasing {
		return asError(err)
	}
	ref := ""
	if c.ResolutionKind != nil {
		ref = *c.ResolutionKind
		if c.ResolutionRef != nil {
			ref += ":" + *c.ResolutionRef
		}
	}
	if _, err := uc.Holds.ReleaseSettlementHold(ctx, hold.HoldID, adapter.HoldRelease{OperationID: "support_case_closed:" + c.ID,
		SourceVersion: c.Version, ResolutionRef: ref, Reason: "Support case closed"}); err != nil {
		if isPermanent(err) {
			uc.Log.Error().Err(err).Str("case_id", c.ID).Str("hold_id", hold.HoldID).Msg("order_support_hold_release_refused")
		}
		return err
	}
	return uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		_, err := uc.CaseHolds.SetStatus(ctx, c.ID, []string{domain.HoldReleasing}, domain.HoldReleased, nil)
		return err
	})
}

// EnsureSupportHolds is the worker's safety net: open cases that may
// affect money get a hold (cases opened before the ledger was on), and
// closed cases whose hold was never sent for release get released.
func (uc *OrderUseCase) EnsureSupportHolds(ctx context.Context, limit int) (int, error) {
	if uc.CaseHolds == nil || uc.Holds == nil {
		return 0, nil
	}
	done := 0
	if uc.holdLedger() {
		missing, err := uc.CaseHolds.ListMissing(ctx, limit)
		if err != nil {
			return 0, err
		}
		for _, ref := range missing {
			err := uc.withOrder(ctx, ref.OrderID, func(ctx context.Context) error {
				c, err := uc.Support.FindByID(ctx, ref.CaseID)
				if err != nil || c.Status == domain.CaseClosed {
					return err
				}
				return uc.prepareCaseHold(ctx, c)
			})
			if err != nil {
				return done, err
			}
			done++
		}
	}
	unreleased, err := uc.CaseHolds.ListUnreleased(ctx, limit)
	if err != nil {
		return done, err
	}
	for _, ref := range unreleased {
		err := uc.withOrder(ctx, ref.OrderID, func(ctx context.Context) error {
			c, err := uc.Support.FindByID(ctx, ref.CaseID)
			if err != nil || c.Status != domain.CaseClosed {
				return err
			}
			return uc.releaseCaseHoldOnClose(ctx, c)
		})
		if err != nil {
			return done, err
		}
		done++
	}
	if done > 0 {
		uc.runEffectsSoon(ctx, "")
	}
	return done, nil
}

// reportSupportHolds logs holds waiting for Payment or for an operator.
func (uc *OrderUseCase) reportSupportHolds(ctx context.Context) {
	if uc.CaseHolds == nil {
		return
	}
	preparing, review, releasing, err := uc.CaseHolds.Counts(ctx)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			uc.Log.Error().Err(err).Msg("order_support_hold_report_failed")
		}
		return
	}
	if preparing+review+releasing == 0 {
		return
	}
	levelFor(uc.Log, review > 0).Int64("preparing", preparing).Int64("needs_review", review).Int64("releasing", releasing).
		Msg("order_support_hold_backlog")
}
