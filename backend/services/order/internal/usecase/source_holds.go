package usecase

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// SourceHoldPort stores the settlement holds of returns and refunds
// (PW-001).
type SourceHoldPort interface {
	Get(ctx context.Context, sourceType, sourceID string) (*domain.SourceHold, error)
	Prepare(ctx context.Context, h *domain.SourceHold) (bool, error)
	SetStatus(ctx context.Context, sourceType, sourceID string, from []string, to string, note *string) (bool, error)
	ListMissing(ctx context.Context, limit int) ([]repository.SourceRef, error)
	ListUnreleased(ctx context.Context, limit int) ([]repository.SourceRef, error)
	Counts(ctx context.Context) (repository.SourceHoldCounts, error)
}

// Effect targets of return and refund holds ("return:<id>", "refund:<id>").
const (
	returnHoldTarget = "return:"
	refundHoldTarget = "refund:"
)

// sourceHoldTarget splits a hold effect's target into source type and id.
func sourceHoldTarget(target string) (string, string, bool) {
	if id, ok := strings.CutPrefix(target, returnHoldTarget); ok {
		return domain.HoldSourceReturn, id, true
	}
	if id, ok := strings.CutPrefix(target, refundHoldTarget); ok {
		return domain.HoldSourceRefund, id, true
	}
	return "", "", false
}

// sourceHoldLedger: with FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED new returns
// and refunds get a hold; holds that exist are always released.
func (uc *OrderUseCase) sourceHoldLedger() bool {
	return uc.SupportConfig.HoldLedger && uc.SourceHolds != nil && uc.Holds != nil
}

// prepareSourceHold records a preparing hold for a return or refund of a
// vendor order and queues its acquire, in the caller's transaction (the one
// that opens the return or refund), so the payout is held from the moment
// the source exists.
func (uc *OrderUseCase) prepareSourceHold(ctx context.Context, sourceType, sourceID, orderID, vendorID, vendorOrderID string) error {
	if !uc.sourceHoldLedger() {
		return nil
	}
	prepared, err := uc.SourceHolds.Prepare(ctx, &domain.SourceHold{SourceType: sourceType, SourceID: sourceID, OrderID: orderID,
		VendorID: vendorID, VendorOrderID: vendorOrderID, HoldID: uuid.NewString()})
	if err != nil || !prepared {
		return err
	}
	return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: orderID, Kind: domain.EffectAcquireSettlementHold, Target: sourceType + ":" + sourceID})
}

// prepareRefundHold holds the payout of a refund's vendor order (an
// order-level refund without a vendor order holds nothing, as before).
func (uc *OrderUseCase) prepareRefundHold(ctx context.Context, refund *domain.Refund) error {
	if !uc.sourceHoldLedger() || refund.VendorOrderID == nil {
		return nil
	}
	vo, err := uc.VendorOrders.FindByID(ctx, *refund.VendorOrderID)
	if err != nil {
		return err
	}
	return uc.prepareSourceHold(ctx, domain.HoldSourceRefund, refund.ID, refund.OrderID, vo.VendorID, vo.ID)
}

// sourceVersion is what the source tells Payment about its version.
func (uc *OrderUseCase) sourceVersion(ctx context.Context, h *domain.SourceHold) (int64, string, error) {
	if h.SourceType == domain.HoldSourceRefund {
		r, err := uc.Refunds.FindByID(ctx, h.SourceID)
		if err != nil {
			return 0, "", err
		}
		return 1, string(r.Status), nil
	}
	rr, err := uc.Returns.FindByID(ctx, h.SourceID)
	if err != nil {
		return 0, "", err
	}
	return rr.Version, string(rr.Status), nil
}

// acquireSourceHold is the acquire effect of a return or refund hold. The
// answer is recorded under the order lock and only moves a hold still
// preparing; a refusal (payout_already_claimed) needs an operator.
func (uc *OrderUseCase) acquireSourceHold(ctx context.Context, sourceType, sourceID string) error {
	if uc.SourceHolds == nil || uc.Holds == nil {
		return errHoldsNotWired
	}
	h, err := uc.SourceHolds.Get(ctx, sourceType, sourceID)
	if err != nil || h == nil || h.Status != domain.HoldPreparing {
		return asError(err)
	}
	version, _, err := uc.sourceVersion(ctx, h)
	if err != nil {
		return appError(err)
	}
	receipt, err := uc.Holds.AcquireSettlementHold(ctx, adapter.HoldRequest{HoldID: h.HoldID, VendorID: h.VendorID, VendorOrderID: h.VendorOrderID,
		SourceType: h.PaymentSourceType(), SourceID: h.SourceID, SourceVersion: version, ReasonCode: h.ReasonCode()})
	to, note := domain.HoldActive, (*string)(nil)
	switch {
	case err == nil && receipt.Status == "released":
		to = domain.HoldReleased
	case err == nil:
	case isPermanent(err):
		message := appError(err).Message
		to, note = domain.HoldNeedsReview, &message
	default:
		return err
	}
	err = uc.withOrder(ctx, h.OrderID, func(ctx context.Context) error {
		_, err := uc.SourceHolds.SetStatus(ctx, sourceType, sourceID, []string{domain.HoldPreparing}, to, note)
		return err
	})
	if err != nil {
		return err
	}
	event := uc.Log.Info()
	if to == domain.HoldNeedsReview {
		event = uc.Log.Warn()
	}
	event.Str("source_type", sourceType).Str("source_id", sourceID).Str("vendor_order_id", h.VendorOrderID).Str("hold_status", to).
		Msg("order_source_hold_recorded")
	return nil
}

// releaseSourceHold is the release effect, once the source is final.
func (uc *OrderUseCase) releaseSourceHold(ctx context.Context, sourceType, sourceID string) error {
	if uc.SourceHolds == nil || uc.Holds == nil {
		return errHoldsNotWired
	}
	h, err := uc.SourceHolds.Get(ctx, sourceType, sourceID)
	if err != nil || h == nil || h.Status != domain.HoldReleasing {
		return asError(err)
	}
	version, status, err := uc.sourceVersion(ctx, h)
	if err != nil {
		return appError(err)
	}
	if _, err := uc.Holds.ReleaseSettlementHold(ctx, h.HoldID, adapter.HoldRelease{OperationID: sourceType + "_final:" + sourceID,
		SourceVersion: version, ResolutionRef: sourceType + ":" + status, Reason: strings.ToUpper(sourceType[:1]) + sourceType[1:] + " " + status}); err != nil {
		if isPermanent(err) {
			uc.Log.Error().Err(err).Str("source_type", sourceType).Str("source_id", sourceID).Msg("order_source_hold_release_refused")
		}
		return err
	}
	return uc.withOrder(ctx, h.OrderID, func(ctx context.Context) error {
		_, err := uc.SourceHolds.SetStatus(ctx, sourceType, sourceID, []string{domain.HoldReleasing}, domain.HoldReleased, nil)
		return err
	})
}

// EnsureSourceHolds is the sweep: with the ledger on, open returns and
// refunds without a hold get one (the backfill of sources opened before
// the flag); holds whose source is final are sent for release (always,
// also with the flag off).
func (uc *OrderUseCase) EnsureSourceHolds(ctx context.Context, limit int) (int, error) {
	if uc.SourceHolds == nil || uc.Holds == nil {
		return 0, nil
	}
	done := 0
	if uc.sourceHoldLedger() {
		missing, err := uc.SourceHolds.ListMissing(ctx, limit)
		if err != nil {
			return 0, err
		}
		for _, ref := range missing {
			if err := uc.withOrder(ctx, ref.OrderID, func(ctx context.Context) error { return uc.backfillSourceHold(ctx, ref) }); err != nil {
				return done, err
			}
			done++
		}
	}
	final, err := uc.SourceHolds.ListUnreleased(ctx, limit)
	if err != nil {
		return done, err
	}
	for _, ref := range final {
		err := uc.withOrder(ctx, ref.OrderID, func(ctx context.Context) error {
			moved, err := uc.SourceHolds.SetStatus(ctx, ref.SourceType, ref.SourceID,
				[]string{domain.HoldPreparing, domain.HoldActive, domain.HoldNeedsReview}, domain.HoldReleasing, nil)
			if err != nil || !moved {
				return err
			}
			return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: ref.OrderID, Kind: domain.EffectReleaseSettlementHold, Target: ref.SourceType + ":" + ref.SourceID})
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

// backfillSourceHold prepares the hold of an open source found by the
// sweep, re-reading it under the order lock.
func (uc *OrderUseCase) backfillSourceHold(ctx context.Context, ref repository.SourceRef) error {
	if ref.SourceType == domain.HoldSourceRefund {
		r, err := uc.Refunds.FindByID(ctx, ref.SourceID)
		if err != nil || (r.Status != domain.RefundRequested && r.Status != domain.RefundSubmitted) {
			return err
		}
		return uc.prepareRefundHold(ctx, r)
	}
	rr, err := uc.Returns.FindByID(ctx, ref.SourceID)
	if err != nil || rr.Status == domain.ReturnRejected || rr.Status == domain.ReturnRefunded {
		return err
	}
	item, err := uc.Orders.FindItem(ctx, rr.OrderID, rr.OrderItemID)
	if err != nil {
		return err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, item.VendorOrderID)
	if err != nil {
		return err
	}
	return uc.prepareSourceHold(ctx, domain.HoldSourceReturn, rr.ID, rr.OrderID, vo.VendorID, vo.ID)
}

// reportSourceHolds logs holds waiting on Payment or an operator, and
// open sources still without a hold. missing must reach zero (with the
// ledger on) before Payment stops asking Order for holds.
func (uc *OrderUseCase) reportSourceHolds(ctx context.Context) {
	if uc.SourceHolds == nil {
		return
	}
	c, err := uc.SourceHolds.Counts(ctx)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			uc.Log.Error().Err(err).Msg("order_source_hold_report_failed")
		}
		return
	}
	if c.Preparing+c.NeedsReview+c.Releasing+c.Missing == 0 {
		return
	}
	levelFor(uc.Log, c.NeedsReview > 0 || (uc.sourceHoldLedger() && c.Missing > 0)).Int64("preparing", c.Preparing).
		Int64("needs_review", c.NeedsReview).Int64("releasing", c.Releasing).Int64("missing", c.Missing).
		Bool("ledger", uc.sourceHoldLedger()).Msg("order_source_hold_backlog")
}
