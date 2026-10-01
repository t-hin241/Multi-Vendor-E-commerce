package usecase

import (
	"context"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// cancelLocked cancels an unpaid order in the caller's transaction (the
// order lock is held): the order and every vendor order move to cancelled
// together, and releasing the stock hold, voiding any shipment and telling
// the buyer are queued as durable effects.
func (uc *OrderUseCase) cancelLocked(ctx context.Context, order *domain.Order, reason string, notify bool) error {
	if err := uc.Orders.TransitionStatus(ctx, order.ID, order.Status, domain.StatusCancelled, &reason); err != nil {
		return err
	}
	if _, err := uc.VendorOrders.TransitionAllForOrder(ctx, order.ID, domain.StatusPendingPayment, domain.StatusCancelled); err != nil {
		return err
	}
	if order.CheckoutState == domain.CheckoutPreparing {
		if err := uc.Orders.SetCheckoutState(ctx, order.ID, domain.CheckoutFailed); err != nil {
			return err
		}
		order.CheckoutState = domain.CheckoutFailed
	}
	vendorOrders, err := uc.VendorOrders.ListByOrderID(ctx, order.ID)
	if err != nil {
		return err
	}
	effects := []domain.Effect{{OrderID: order.ID, Kind: domain.EffectReleaseInventory}}
	for _, vo := range vendorOrders {
		// Orders created before this upgrade may have a pending shipment.
		effects = append(effects, domain.Effect{OrderID: order.ID, Kind: domain.EffectCancelShipment, Target: vo.ID})
	}
	if notify {
		effects = append(effects, domain.NewNotifyEffect(order.ID, order.BuyerID, notifyOrderCancelled))
	}
	if err := uc.Effects.Enqueue(ctx, effects...); err != nil {
		return err
	}
	order.Status, order.CancellationReason = domain.StatusCancelled, &reason
	return nil
}

// Cancel lets a buyer cancel their own unpaid order.
func (uc *OrderUseCase) Cancel(ctx context.Context, buyerID, orderID string) (*domain.Order, error) {
	var order *domain.Order
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		var err error
		if order, err = uc.findOwnedByBuyer(ctx, buyerID, orderID); err != nil {
			return err
		}
		if !domain.CanTransition(order.Status, domain.StatusCancelled) {
			return apperror.Conflict("This order can no longer be cancelled")
		}
		return uc.cancelLocked(ctx, order, "Cancelled by buyer", true)
	})
	if err != nil {
		return nil, err
	}
	uc.runEffectsSoon(ctx, orderID)
	return order, nil
}

// AdminCancel cancels an unpaid order as an admin intervention. Paid
// orders are never "cancelled" into a refund: money goes back only through
// a refund Payment confirms (AdminRequestRefund).
func (uc *OrderUseCase) AdminCancel(ctx context.Context, adminID, orderID, reason string) (*domain.Order, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return nil, apperror.Validation("A reason of at most 500 characters is required")
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var order *domain.Order
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		var err error
		if order, err = uc.findOrder(ctx, orderID); err != nil {
			return err
		}
		if !domain.CanTransition(order.Status, domain.StatusCancelled) {
			return apperror.Conflict("Only an unpaid order can be cancelled; use a refund for a paid order")
		}
		return uc.cancelLocked(ctx, order, reason, true)
	})
	if err != nil {
		return nil, err
	}
	uc.Log.Info().Str("order_id", orderID).Str("admin_id", adminID).Msg("order_admin_cancelled")
	uc.runEffectsSoon(ctx, orderID)
	return order, nil
}

// UpdateVendorOrderStatus lets a vendor start preparing a paid sub-order
// (processing). Shipped and completed are not set by hand: they follow the
// shipment (ApplyShipmentEvent), so Order and Shipment cannot disagree.
func (uc *OrderUseCase) UpdateVendorOrderStatus(ctx context.Context, userID, vendorOrderID string, newStatus domain.Status) (*domain.VendorOrder, error) {
	if newStatus != domain.StatusProcessing {
		return nil, apperror.Validation("Mark the shipment shipped or delivered instead; only processing is set here")
	}
	vo, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Order not found")
	}
	if _, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vo.VendorID); err != nil {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	err = uc.withOrder(ctx, vo.OrderID, func(ctx context.Context) error {
		current, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
		if err != nil {
			return err
		}
		if current.Status == domain.StatusProcessing {
			vo = current
			return nil
		}
		if err := uc.advanceVendorOrder(ctx, current, domain.StatusProcessing); err != nil {
			return err
		}
		vo = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	uc.runEffectsSoon(ctx, vo.OrderID)
	return vo, nil
}

// advanceVendorOrder moves a locked vendor order one step with
// compare-and-set, recomputes the parent (weakest link) and queues what the
// step implies: buyer notices, and the settlement report on completion.
func (uc *OrderUseCase) advanceVendorOrder(ctx context.Context, current *domain.VendorOrder, newStatus domain.Status) error {
	if !domain.CanTransition(current.Status, newStatus) {
		return apperror.Conflict("Cannot move this order from " + string(current.Status) + " to " + string(newStatus))
	}
	if err := uc.VendorOrders.TransitionStatus(ctx, current.ID, current.Status, newStatus); err != nil {
		return err
	}
	if err := uc.recomputeOrderStatus(ctx, current.OrderID); err != nil {
		return err
	}
	if notifType, ok := map[domain.Status]string{domain.StatusShipped: notifyOrderShipped, domain.StatusCompleted: notifyOrderCompleted}[newStatus]; ok {
		parent, err := uc.findOrder(ctx, current.OrderID)
		if err != nil {
			return err
		}
		e := domain.NewNotifyEffect(current.OrderID, parent.BuyerID, notifType)
		e.Target = notifType + ":" + current.ID // one notice per vendor package
		if err := uc.Effects.Enqueue(ctx, e); err != nil {
			return err
		}
	}
	if newStatus == domain.StatusCompleted {
		// Payment's settlement ledger learns of the sale once, durably.
		if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: current.OrderID, Kind: domain.EffectSettleVendorOrder, Target: current.ID}); err != nil {
			return err
		}
	}
	current.Status = newStatus
	return nil
}

// ShipmentEvent is a fulfillment fact Shipment reports.
type ShipmentEvent struct {
	EventID       string
	ShipmentID    string
	VendorOrderID string
	Type          string
	OccurredAt    time.Time
}

// shipmentPaths are the steps a vendor order takes to reflect a shipment
// event from each status. An event already reflected is a no-op; a vendor
// order that is cancelled or refunded refuses it (Shipment parks it for an
// operator).
var shipmentPaths = map[string]map[domain.Status][]domain.Status{
	"shipped": {
		domain.StatusPaid:       {domain.StatusProcessing, domain.StatusShipped},
		domain.StatusProcessing: {domain.StatusShipped},
		domain.StatusShipped:    {},
		domain.StatusCompleted:  {},
	},
	"delivered": {
		domain.StatusPaid:       {domain.StatusProcessing, domain.StatusShipped, domain.StatusCompleted},
		domain.StatusProcessing: {domain.StatusShipped, domain.StatusCompleted},
		domain.StatusShipped:    {domain.StatusCompleted},
		domain.StatusCompleted:  {},
	},
}

// ApplyShipmentEvent is how a vendor order becomes shipped or completed:
// Order decides from Shipment's facts, in its own transaction. A repeated
// or out-of-order event converges on the same state; "returned" changes no
// status (money and stock are decided by an operator through a refund).
func (uc *OrderUseCase) ApplyShipmentEvent(ctx context.Context, e ShipmentEvent) error {
	vo, err := uc.VendorOrders.FindByID(ctx, e.VendorOrderID)
	if err != nil {
		return notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Vendor order not found")
	}
	logger := uc.Log.With().Str("vendor_order_id", vo.ID).Str("shipment_id", e.ShipmentID).Str("event", e.Type).Logger()
	if e.Type == "returned" {
		logger.Warn().Str("order_id", vo.OrderID).Msg("order_shipment_returned")
		return nil
	}
	paths, ok := shipmentPaths[e.Type]
	if !ok {
		return apperror.Validation("Unknown shipment event type")
	}
	err = uc.withOrder(ctx, vo.OrderID, func(ctx context.Context) error {
		current, err := uc.VendorOrders.FindByID(ctx, vo.ID)
		if err != nil {
			return err
		}
		steps, ok := paths[current.Status]
		if !ok {
			return apperror.Conflict("This vendor order is " + string(current.Status) + "; the shipment event needs review")
		}
		for _, step := range steps {
			if err := uc.advanceVendorOrder(ctx, current, step); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Warn().Err(err).Msg("order_shipment_event_refused")
		return err
	}
	logger.Info().Msg("order_shipment_event_applied")
	uc.runEffectsSoon(ctx, vo.OrderID)
	return nil
}

// recomputeOrderStatus derives the parent status from its vendor orders
// (weakest link) and applies it with compare-and-set.
func (uc *OrderUseCase) recomputeOrderStatus(ctx context.Context, orderID string) error {
	order, err := uc.Orders.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.Status == domain.StatusPendingPayment || order.Status == domain.StatusCancelled {
		return nil
	}
	vendorOrders, err := uc.VendorOrders.ListByOrderID(ctx, orderID)
	if err != nil {
		return err
	}
	statuses := make([]domain.Status, len(vendorOrders))
	for i, vo := range vendorOrders {
		statuses[i] = vo.Status
	}
	target := domain.AggregateStatus(statuses)
	if target == order.Status || !domain.CanTransition(order.Status, target) {
		return nil
	}
	return uc.Orders.TransitionStatus(ctx, orderID, order.Status, target, nil)
}

// SetCommissionRule adds a new commission rule version. Existing orders
// keep the version they snapshotted at checkout.
func (uc *OrderUseCase) SetCommissionRule(ctx context.Context, adminUserID string, rateBps int) (*domain.CommissionRule, error) {
	if err := domain.ValidateCommissionRateBps(rateBps); err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminUserID); err != nil {
		return nil, err
	}
	rule := &domain.CommissionRule{RateBps: rateBps, CreatedBy: &adminUserID}
	if err := uc.CommissionRules.Create(ctx, rule); err != nil {
		return nil, apperror.Internal(err)
	}
	return rule, nil
}

func (uc *OrderUseCase) ListCommissionRules(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error) {
	rules, err := uc.CommissionRules.List(ctx, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return rules, nil
}
