package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// inlineEffectTimeout bounds the effects run right after a transition in
// the caller's request; the worker takes over anything left.
const inlineEffectTimeout = 3 * time.Second

// ProcessEffects runs up to limit due effects (only orderID's when set).
func (uc *OrderUseCase) ProcessEffects(ctx context.Context, orderID string, limit int) (int, error) {
	due, err := uc.Effects.ClaimDue(ctx, orderID, limit)
	if err != nil {
		return 0, err
	}
	for _, e := range due {
		uc.runEffect(ctx, e)
	}
	return len(due), nil
}

// runEffectsSoon tries an order's fresh effects once, without failing or
// delaying the caller beyond a short timeout.
func (uc *OrderUseCase) runEffectsSoon(ctx context.Context, orderID string) {
	if repository.InTransaction(ctx) {
		// Called while applying an event: the effects are not committed
		// yet; the worker runs them right after.
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inlineEffectTimeout)
	defer cancel()
	if _, err := uc.ProcessEffects(ctx, orderID, 20); err != nil {
		uc.Log.Warn().Err(err).Str("order_id", orderID).Msg("order_effects_inline_failed")
	}
}

func (uc *OrderUseCase) runEffect(ctx context.Context, e *domain.Effect) {
	err := uc.executeEffect(ctx, e)
	logger := uc.Log.With().Str("effect_id", e.ID).Str("order_id", e.OrderID).Str("kind", string(e.Kind)).Str("target", e.Target).Logger()
	if err == nil {
		if markErr := uc.Effects.MarkDone(ctx, e.ID); markErr != nil {
			logger.Error().Err(markErr).Msg("order_effect_mark_failed")
		}
		return
	}
	permanent := isPermanent(err)
	parked, recordErr := uc.Effects.RecordFailure(ctx, e.ID, err.Error(), permanent)
	if recordErr != nil {
		logger.Error().Err(recordErr).Msg("order_effect_record_failed")
	}
	if parked {
		logger.Error().Err(err).Int("attempts", e.Attempts+1).Msg("order_effect_parked")
		return
	}
	logger.Warn().Err(err).Int("attempts", e.Attempts+1).Msg("order_effect_retry")
}

// isPermanent: the other service answered and refused (4xx other than
// 401/403/408/429) — repeating the same call cannot succeed.
func isPermanent(err error) bool {
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code == apperror.CodeInternal {
		return false
	}
	switch app.Status {
	case 401, 403, 408, 429:
		return false
	}
	return app.Status >= 400 && app.Status < 500
}

func (uc *OrderUseCase) executeEffect(ctx context.Context, e *domain.Effect) error {
	switch e.Kind {
	case domain.EffectCreateShipment:
		return uc.createShipment(ctx, e)
	case domain.EffectCancelShipment:
		if uc.Events != nil {
			return uc.publish(ctx, e, func() (eventbus.Envelope, error) {
				return events.FulfillmentCancelledEvent(e.ID, events.FulfillmentCancellation{VendorOrderID: e.Target})
			})
		}
		return uc.Shipments.CancelForVendorOrder(ctx, e.Target)
	case domain.EffectReleaseInventory:
		return uc.Inventory.Release(ctx, e.OrderID)
	case domain.EffectNotify:
		var p domain.NotifyPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.UserID == "" {
			return apperror.Validation("invalid notify payload")
		}
		if uc.Events != nil {
			return uc.publish(ctx, e, func() (eventbus.Envelope, error) {
				return events.OrderNotification(e.ID, e.OrderID, events.NotificationRequest{UserID: p.UserID, Type: p.Type, ReferenceID: e.OrderID})
			})
		}
		return uc.Notifications.Notify(ctx, e.ID, p.UserID, p.Type, e.OrderID)
	case domain.EffectRequestRefund:
		return uc.submitRefund(ctx, e.Target)
	case domain.EffectRestockReturn:
		return uc.restockReturn(ctx, e.Target)
	case domain.EffectSettleVendorOrder:
		return uc.settleVendorOrder(ctx, e)
	case domain.EffectReportRejectedOutcome:
		var p domain.RejectedOutcomePayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return apperror.Validation("invalid rejected outcome payload")
		}
		if uc.Events == nil {
			return apperror.Internal(errors.New("event bus publishing is off; Payment cannot be told"))
		}
		return uc.publish(ctx, e, func() (eventbus.Envelope, error) {
			return events.OutcomeRejectedEvent(e.ID, events.OutcomeRejection{Kind: p.Kind, PaymentID: p.PaymentID,
				PaymentRefundID: p.PaymentRefundID, OrderID: e.OrderID, Reason: p.Reason})
		})
	}
	return apperror.Validation("unknown effect kind " + string(e.Kind))
}

// settleVendorOrder reports a completed vendor order with its checkout
// snapshot. Its sales become payable when the return window ends. A vendor
// order without a commission snapshot cannot be settled automatically: the
// effect parks for an operator (a manual ledger adjustment in Payment).
func (uc *OrderUseCase) settleVendorOrder(ctx context.Context, e *domain.Effect) error {
	vo, err := uc.VendorOrders.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if vo.CompletedAt == nil {
		return nil // never completed: nothing to settle
	}
	if vo.Commission == nil {
		return &apperror.Error{Code: apperror.CodeConflict, Status: 409, Message: "vendor order has no commission snapshot; settle it manually"}
	}
	completed := vo.CompletedAt.UTC()
	if uc.Events != nil {
		return uc.publish(ctx, e, func() (eventbus.Envelope, error) {
			return events.VendorOrderSettleableEvent(e.ID, events.Settlement{
				VendorOrderID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Currency: vo.Currency,
				SubtotalAmount: vo.SubtotalAmount, ShippingAmount: vo.ShippingFeeAmount, CommissionAmount: vo.Commission.Amount,
				CommissionRateBps: vo.Commission.RateBps, CommissionRuleVersion: vo.Commission.RuleVersion,
				CompletedAt: completed, EligibleAt: completed.Add(time.Duration(uc.ReturnPolicy.WindowDays) * 24 * time.Hour),
			})
		})
	}
	return uc.Payment.SettleVendorOrder(ctx, adapter.SettlementReport{
		VendorOrderID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Currency: vo.Currency,
		SubtotalAmount: vo.SubtotalAmount, ShippingAmount: vo.ShippingFeeAmount, CommissionAmount: vo.Commission.Amount,
		CommissionRateBps: vo.Commission.RateBps, CommissionRuleVersion: vo.Commission.RuleVersion,
		CompletedAt: completed, EligibleAt: completed.Add(time.Duration(uc.ReturnPolicy.WindowDays) * 24 * time.Hour),
	})
}

// SettlementHolds tells Payment which vendor orders must not be paid out
// yet: those with a return or refund still open.
func (uc *OrderUseCase) SettlementHolds(ctx context.Context, vendorOrderIDs []string) (map[string]string, error) {
	if len(vendorOrderIDs) > 500 {
		return nil, apperror.Validation("At most 500 vendor orders per request")
	}
	held, err := uc.VendorOrders.HeldForSettlement(ctx, vendorOrderIDs)
	if err != nil {
		return nil, appError(err)
	}
	return held, nil
}

// createShipment opens the shipment of a paid vendor order with the fee
// the buyer paid. A vendor order that is no longer fulfillable (cancelled,
// refunded) needs no shipment.
func (uc *OrderUseCase) createShipment(ctx context.Context, e *domain.Effect) error {
	vo, err := uc.VendorOrders.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if !vo.Fulfillable() {
		return nil
	}
	order, err := uc.findOrder(ctx, vo.OrderID)
	if err != nil {
		return err
	}
	in := adapter.CreateShipmentInput{
		VendorOrderID: vo.ID, VendorID: vo.VendorID, BuyerID: order.BuyerID,
		RecipientName: order.RecipientName, Phone: order.Phone, Province: order.Province,
		District: order.District, Ward: order.Ward, StreetAddress: order.StreetAddress,
	}
	if vo.Shipping != nil {
		in.PackageWeightGrams = vo.Shipping.PackageWeightGrams
		in.Quote = &adapter.QuotedFeeRef{FeeAmount: vo.Shipping.FeeAmount, CarrierID: vo.Shipping.CarrierID, ZoneID: vo.Shipping.ZoneID, FeeRuleID: vo.Shipping.FeeRuleID}
	} else if in.PackageWeightGrams, err = uc.legacyPackageWeight(ctx, vo.ID); err != nil {
		return err
	}
	if uc.Events != nil {
		return uc.publish(ctx, e, func() (eventbus.Envelope, error) {
			f := events.Fulfillment{VendorOrderID: in.VendorOrderID, VendorID: in.VendorID, BuyerID: in.BuyerID,
				PackageWeightGrams: in.PackageWeightGrams, RecipientName: in.RecipientName, Phone: in.Phone, Province: in.Province,
				District: in.District, Ward: in.Ward, StreetAddress: in.StreetAddress}
			if q := in.Quote; q != nil {
				f.Quote = &events.QuotedFee{FeeAmount: q.FeeAmount, CarrierID: q.CarrierID, ZoneID: q.ZoneID, FeeRuleID: q.FeeRuleID}
			}
			return events.FulfillmentReadyEvent(e.ID, order.ID, f)
		})
	}
	_, err = uc.Shipments.CreateShipment(ctx, in)
	return err
}

// publish sends an effect as an event (the effect id is the event id, so
// a retry is the same event); done once the broker stored it.
func (uc *OrderUseCase) publish(ctx context.Context, e *domain.Effect, build func() (eventbus.Envelope, error)) error {
	env, err := build()
	if err != nil {
		return apperror.Validation(err.Error())
	}
	if err := uc.Events.Publish(ctx, env.WithCorrelation(e.ID)); err != nil {
		if eventbus.IsPermanent(err) {
			return apperror.Validation(err.Error())
		}
		return apperror.Internal(err)
	}
	return nil
}

// legacyPackageWeight recomputes a pre-upgrade vendor order's weight from
// the catalog (no snapshot was taken then).
func (uc *OrderUseCase) legacyPackageWeight(ctx context.Context, vendorOrderID string) (int64, error) {
	items, err := uc.VendorOrders.ListItemsByVendorOrderIDs(ctx, []string{vendorOrderID})
	if err != nil {
		return 0, appError(err)
	}
	var weight int64
	for _, item := range items[vendorOrderID] {
		product, err := uc.Catalog.GetProduct(ctx, item.ProductID)
		if err != nil || product.PackageWeightGrams == nil {
			continue
		}
		weight += *product.PackageWeightGrams * item.Quantity
	}
	return weight, nil
}

// EffectBacklog is the monitoring snapshot of pending/parked effects.
func (uc *OrderUseCase) EffectBacklog(ctx context.Context) (domain.EffectStats, error) {
	return uc.Effects.Stats(ctx)
}

// OperationCounts is the work waiting for an operator (empty when not
// configured).
func (uc *OrderUseCase) OperationCounts(ctx context.Context) (map[string]int64, error) {
	if uc.Operations == nil {
		return map[string]int64{}, nil
	}
	counts, err := uc.Operations.Counts(ctx)
	return counts, asError(err)
}

// ListParkedEffects returns effects that ran out of retries (admin).
func (uc *OrderUseCase) ListParkedEffects(ctx context.Context, limit, offset int) ([]*domain.Effect, error) {
	effects, err := uc.Effects.ListParked(ctx, limit, offset)
	return effects, asError(err)
}

// ReplayEffect puts a parked effect back in the queue after the cause was
// fixed. It needs a reason and is audited; replaying an effect that is
// already queued or done changes nothing and reports false, so a resend
// is harmless (each effect is itself idempotent at its target).
func (uc *OrderUseCase) ReplayEffect(ctx context.Context, adminID, effectID, reason string) (bool, error) {
	note, err := adminReason(reason)
	if err != nil {
		return false, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return false, err
	}
	effect, err := uc.Effects.Find(ctx, effectID)
	if err != nil {
		return false, notFoundOrInternal(err, repository.ErrEffectNotFound, "Side effect not found")
	}
	replayed := false
	err = uc.withOrder(ctx, effect.OrderID, func(ctx context.Context) error {
		ok, err := uc.Effects.Replay(ctx, effectID)
		if err != nil || !ok {
			return err
		}
		replayed = true
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "effect_replayed", EntityType: domain.AuditEffect, EntityID: effectID,
			OrderID: &effect.OrderID, Reason: note, Changes: map[string]any{"kind": effect.Kind, "status": domain.Change("parked", "pending")}})
	})
	if err != nil {
		return false, err
	}
	if replayed {
		uc.Log.Info().Str("effect_id", effectID).Str("admin_id", adminID).Msg("order_effect_replayed")
		uc.runEffectsSoon(ctx, effect.OrderID)
	}
	return replayed, nil
}
