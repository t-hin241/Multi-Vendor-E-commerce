package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
)

// MarkPaid applies a capture Payment reported. Order checks it against its
// own snapshot: the amount and currency must match the order total, the
// order must be ready and still awaiting payment, and no other capture may
// already have paid it. A capture that fails any check is recorded as
// rejected (money to refund or review) and answered with 409; nothing is
// marked paid. Inventory commit happens before the paid transition; the
// parent order, every vendor order, the capture record and the
// fulfillment/notification effects then change in one transaction.
//
// capture is nil only for Payment builds from before this contract, which
// verified the amount themselves.
func (uc *OrderUseCase) MarkPaid(ctx context.Context, orderID string, capture *domain.PaymentCapture) (*domain.Order, error) {
	var (
		order     *domain.Order
		rejection string
	)
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		var err error
		order, rejection, err = uc.markPaid(ctx, orderID, capture)
		return err
	})
	if err != nil {
		return nil, err
	}
	if rejection != "" {
		ev := uc.Log.Warn().Str("order_id", orderID).Str("reason", rejection)
		if capture != nil {
			ev = ev.Str("payment_id", capture.PaymentID)
		}
		ev.Msg("order_payment_rejected")
		return nil, apperror.Conflict("Payment cannot be applied to this order (" + rejection + "); it is queued for refund review")
	}
	uc.runEffectsSoon(ctx, orderID)
	return order, nil
}

func (uc *OrderUseCase) markPaid(ctx context.Context, orderID string, capture *domain.PaymentCapture) (*domain.Order, string, error) {
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, "", err
	}

	if capture == nil {
		if order.Status.PaidOrFurther() {
			// Legacy retry: make sure the stock sale is committed.
			return order, "", uc.Inventory.Commit(ctx, orderID)
		}
		if !domain.CanTransition(order.Status, domain.StatusPaid) {
			return nil, "", apperror.Conflict("Order cannot be marked paid from status " + string(order.Status))
		}
		if err := uc.Inventory.Commit(ctx, orderID); err != nil {
			return nil, "", err
		}
		return order, "", uc.applyPaid(ctx, order)
	}

	existing, err := uc.Payments.Find(ctx, capture.PaymentID)
	if err != nil {
		return nil, "", err
	}
	if existing != nil {
		if existing.OrderID != orderID {
			return nil, "", apperror.Conflict("Payment belongs to a different order")
		}
		if existing.Outcome == domain.PaymentApplied {
			return order, "", nil // replay of an applied capture
		}
		return order, *existing.RejectionReason, nil
	}

	payments, err := uc.Payments.ListByOrder(ctx, orderID)
	if err != nil {
		return nil, "", err
	}
	alreadyPaid := false
	for _, p := range payments {
		alreadyPaid = alreadyPaid || p.Outcome == domain.PaymentApplied
	}
	reason := domain.JudgeCapture(order, *capture, alreadyPaid)
	if reason == "" {
		if err := uc.Inventory.Commit(ctx, orderID); err != nil {
			var app *apperror.Error
			if !errors.As(err, &app) || app.Code != apperror.CodeConflict {
				return nil, "", err // retryable: nothing recorded
			}
			// Hold expired or released: the stock is gone, so this capture
			// cannot pay for the order.
			reason = domain.RejectStockNotHeld
		}
	}
	record := &domain.OrderPayment{PaymentID: capture.PaymentID, OrderID: orderID, Amount: capture.Amount, Currency: capture.Currency, Outcome: domain.PaymentApplied}
	if reason != "" {
		record.Outcome, record.RejectionReason = domain.PaymentRejected, &reason
	}
	if err := uc.Payments.Insert(ctx, record); err != nil {
		return nil, "", err
	}
	if reason != "" {
		return order, reason, nil
	}
	return order, "", uc.applyPaid(ctx, order)
}

// applyPaid moves the order and all its vendor orders to paid and queues
// fulfillment, in the caller's transaction.
func (uc *OrderUseCase) applyPaid(ctx context.Context, order *domain.Order) error {
	if err := uc.Orders.TransitionStatus(ctx, order.ID, order.Status, domain.StatusPaid, nil); err != nil {
		return err
	}
	if _, err := uc.VendorOrders.TransitionAllForOrder(ctx, order.ID, domain.StatusPendingPayment, domain.StatusPaid); err != nil {
		return err
	}
	vendorOrders, err := uc.VendorOrders.ListByOrderID(ctx, order.ID)
	if err != nil {
		return err
	}
	effects := []domain.Effect{domain.NewNotifyEffect(order.ID, order.BuyerID, notifyOrderPaid)}
	var rule *domain.CommissionRule
	for _, vo := range vendorOrders {
		if vo.Commission == nil {
			// Order created before commission moved to checkout: snapshot
			// the rule current now, as the old flow did, and label it.
			if rule == nil {
				if rule, err = uc.CommissionRules.FindCurrent(ctx); err != nil {
					return err
				}
			}
			amount, net, ok := domain.ComputeCommission(vo.SubtotalAmount, rule.RateBps)
			if !ok {
				return apperror.Internal(errors.New("commission overflow"))
			}
			if err := uc.VendorOrders.SetLegacyCommission(ctx, vo.ID, rule, amount, net, vo.SubtotalAmount); err != nil {
				return err
			}
		}
		effects = append(effects, domain.Effect{OrderID: order.ID, Kind: domain.EffectCreateShipment, Target: vo.ID})
	}
	if err := uc.Effects.Enqueue(ctx, effects...); err != nil {
		return err
	}
	order.Status = domain.StatusPaid
	return nil
}

// MarkPaymentFailed cancels an unpaid order after its payment failed. A
// failure report never cancels an order that is already paid.
func (uc *OrderUseCase) MarkPaymentFailed(ctx context.Context, orderID, reason string) (*domain.Order, error) {
	var order *domain.Order
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		var err error
		if order, err = uc.findOrder(ctx, orderID); err != nil {
			return err
		}
		if order.Status == domain.StatusCancelled {
			return nil
		}
		if !domain.CanTransition(order.Status, domain.StatusCancelled) {
			return apperror.Conflict("Order cannot be cancelled from status " + string(order.Status))
		}
		return uc.cancelLocked(ctx, order, reason, true)
	})
	if err != nil {
		return nil, err
	}
	uc.runEffectsSoon(ctx, orderID)
	return order, nil
}

// ReservationExpired handles Inventory's expiry event after confirming the
// receipt itself.
func (uc *OrderUseCase) ReservationExpired(ctx context.Context, id string) error {
	receipt, err := uc.Inventory.Operation(ctx, id)
	if err != nil {
		return err
	}
	if receipt.Status != "expired" {
		return apperror.Conflict("Reservation expiry is not confirmed")
	}
	_, err = uc.MarkPaymentFailed(ctx, id, "Stock reservation expired")
	return err
}

// Reservation serves Payment's check of the stock hold behind an order.
func (uc *OrderUseCase) Reservation(ctx context.Context, id string) (*adapter.ReservationReceipt, error) {
	return uc.Inventory.Operation(ctx, id)
}

// GetForPayment serves Payment's read of an order before opening an
// intent. An unpaid order that is still being prepared (stock not yet
// reserved) is refused, so no intent is opened for it.
func (uc *OrderUseCase) GetForPayment(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status == domain.StatusPendingPayment && order.CheckoutState != domain.CheckoutReady {
		return nil, domain.OrderNotReady()
	}
	return order, nil
}
