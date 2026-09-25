// Package usecase orchestrates Order's workflows: checkout (the saga that
// spans Cart, Catalog and Inventory), order status transitions, and the
// buyer/vendor-scoped views.
package usecase

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// variantLabel joins a variant's option labels into one display string,
// e.g. "Color: Red, Size: L", snapshotted onto the order item at checkout.
func variantLabel(options []adapter.VariantOptionInfo) string {
	parts := make([]string, 0, len(options))
	for _, o := range options {
		parts = append(parts, o.AttributeName+": "+o.OptionValue)
	}
	return strings.Join(parts, ", ")
}

type OrderUseCase struct {
	orders          OrderRepositoryPort
	vendorOrders    VendorOrderRepositoryPort
	buyerAddresses  BuyerAddressRepositoryPort
	commissionRules CommissionRuleRepositoryPort
	cart            CartGateway
	catalog         CatalogGateway
	vendors         VendorGateway
	inventory       InventoryGateway
	shipments       ShipmentGateway
	notifications   NotificationGateway
	log             zerolog.Logger
}

func NewOrderUseCase(
	orders OrderRepositoryPort,
	vendorOrders VendorOrderRepositoryPort,
	buyerAddresses BuyerAddressRepositoryPort,
	commissionRules CommissionRuleRepositoryPort,
	cart CartGateway,
	catalog CatalogGateway,
	vendors VendorGateway,
	inventory InventoryGateway,
	shipments ShipmentGateway,
	notifications NotificationGateway,
	log zerolog.Logger,
) *OrderUseCase {
	return &OrderUseCase{
		orders: orders, vendorOrders: vendorOrders, buyerAddresses: buyerAddresses, commissionRules: commissionRules,
		cart: cart, catalog: catalog, vendors: vendors, inventory: inventory, shipments: shipments,
		notifications: notifications, log: log,
	}
}

// Notification event types, matching Notification's own vocabulary
// (services/notification/internal/domain.Type). Order doesn't import that
// package — the contract between them is the plain string, not a shared Go
// type — but the values must stay in sync.
const (
	notifyOrderPaid      = "order_paid"
	notifyOrderShipped   = "order_shipped"
	notifyOrderCompleted = "order_completed"
	notifyOrderCancelled = "order_cancelled"
	notifyOrderRefunded  = "order_refunded"
)

// notify is fire-and-forget from every caller's point of view: a
// notification failure is logged and never propagated, since it must never
// roll back the order transition that triggered it.
func (uc *OrderUseCase) notify(ctx context.Context, userID, notifType, referenceID string) {
	if err := uc.notifications.Notify(ctx, userID, notifType, referenceID); err != nil {
		uc.log.Error().Err(err).Str("user_id", userID).Str("type", notifType).Str("reference_id", referenceID).Msg("failed to send notification")
	}
}

// Checkout is the buyer-checkout saga:
//  1. read the cart (selection only — never trust its displayed price),
//  2. resolve the buyer's chosen shipping address,
//  3. re-price and validate every line against Catalog right now, and sum
//     each vendor's package weight along the way,
//  4. persist the order locally (the only step with a real DB transaction),
//  5. reserve stock in Inventory — if this fails, cancel the order we just
//     created and surface why,
//  6. best-effort create a shipment (with its fee already quoted) per
//     vendor sub-order, then snapshot each fee and recompute the order's
//     total — a briefly unreachable Shipment never fails the checkout; the
//     vendor's own manual "create shipment" endpoint is the fallback,
//  7. best-effort clear the cart, since the order and its reservation are
//     already the source of truth at this point.
func (uc *OrderUseCase) Checkout(ctx context.Context, buyerID, bearerToken, addressID string) (*domain.Order, error) {
	cartLines, err := uc.cart.GetItems(ctx, bearerToken)
	if err != nil {
		return nil, err
	}
	if len(cartLines) == 0 {
		return nil, apperror.Validation("Your cart is empty")
	}

	address, err := uc.resolveCheckoutAddress(ctx, buyerID, addressID)
	if err != nil {
		return nil, err
	}

	checkoutLines := make([]domain.CheckoutLine, 0, len(cartLines))
	weightByVendor := map[string]int64{}
	for _, line := range cartLines {
		product, err := uc.catalog.GetProduct(ctx, line.ProductID)
		if err != nil {
			var appErr *apperror.Error
			if errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound {
				return nil, apperror.Validation("A product in your cart no longer exists; please update your cart")
			}
			return nil, err
		}
		if !product.IsVisible {
			return nil, apperror.Validation("\"" + product.Name + "\" is no longer available; please update your cart")
		}
		if product.HasVariants && line.VariantID == nil {
			return nil, apperror.Validation("\"" + product.Name + "\" requires selecting an option; please update your cart")
		}

		checkoutLine := domain.CheckoutLine{
			ProductID:   product.ID,
			VendorID:    product.VendorID,
			ProductName: product.Name,
			PriceAmount: product.PriceAmount,
			Currency:    product.Currency,
			Quantity:    line.Quantity,
		}
		if line.VariantID != nil {
			// Re-verified here, not just trusted from Cart: cart state
			// could be stale by the time checkout runs.
			variant, err := uc.catalog.GetVariant(ctx, *line.VariantID)
			if err != nil {
				var appErr *apperror.Error
				if errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound {
					return nil, apperror.Validation("A selected option in your cart no longer exists; please update your cart")
				}
				return nil, err
			}
			if variant.ProductID != product.ID {
				return nil, apperror.Validation("A selected option in your cart no longer matches its product; please update your cart")
			}
			label := variantLabel(variant.Options)
			checkoutLine.VariantID = line.VariantID
			checkoutLine.VariantSKU = &variant.SKU
			checkoutLine.VariantLabel = &label
		}
		checkoutLines = append(checkoutLines, checkoutLine)

		if product.PackageWeightGrams != nil {
			weightByVendor[product.VendorID] += *product.PackageWeightGrams * line.Quantity
		}
	}

	plan, err := domain.BuildCheckoutPlan(buyerID, checkoutLines)
	if err != nil {
		return nil, err
	}
	plan.Order.RecipientName, plan.Order.Phone = address.RecipientName, address.Phone
	plan.Order.Province, plan.Order.District, plan.Order.Ward, plan.Order.StreetAddress =
		address.Province, address.District, address.Ward, address.StreetAddress

	order, err := uc.orders.CreateFromPlan(ctx, plan)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	reserveLines := make([]adapter.ReserveLine, 0, len(checkoutLines))
	for _, line := range checkoutLines {
		reserveLines = append(reserveLines, adapter.ReserveLine{ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity})
	}

	if err := uc.inventory.Reserve(ctx, order.ID, reserveLines); err != nil {
		reason := "Stock reservation failed at checkout"
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			reason = appErr.Message
		}
		if cancelErr := uc.orders.UpdateStatus(ctx, order.ID, domain.StatusCancelled, &reason); cancelErr != nil {
			uc.log.Error().Err(cancelErr).Str("order_id", order.ID).Msg("failed to cancel order after reservation failure")
		}
		return nil, err
	}

	order.TotalAmount = uc.createShipmentsAndApplyFees(ctx, order, plan, address, weightByVendor)

	if err := uc.cart.Clear(ctx, bearerToken); err != nil {
		uc.log.Error().Err(err).Str("order_id", order.ID).Msg("checkout succeeded but clearing the cart failed")
	}

	return order, nil
}

// resolveCheckoutAddress looks up and verifies ownership of the buyer's
// chosen address — checkout can't proceed without a real destination for
// the shipment(s) it's about to create.
func (uc *OrderUseCase) resolveCheckoutAddress(ctx context.Context, buyerID, addressID string) (*domain.BuyerAddress, error) {
	if addressID == "" {
		return nil, apperror.Validation("Please choose a shipping address before checking out")
	}
	address, err := uc.buyerAddresses.FindByID(ctx, addressID)
	if err != nil {
		return nil, apperror.Validation("Please choose a valid shipping address before checking out")
	}
	if address.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this address")
	}
	return address, nil
}

// createShipmentsAndApplyFees opens a shipment per vendor sub-order
// (best-effort — Shipment being briefly unreachable never fails an
// otherwise-valid checkout) and snapshots each quoted fee onto its vendor
// order, returning the order's new total (product subtotal plus whatever
// shipping fees were successfully quoted).
func (uc *OrderUseCase) createShipmentsAndApplyFees(ctx context.Context, order *domain.Order, plan *domain.Plan, address *domain.BuyerAddress, weightByVendor map[string]int64) int64 {
	vendorOrders, err := uc.vendorOrders.ListByOrderID(ctx, order.ID)
	if err != nil {
		uc.log.Error().Err(err).Str("order_id", order.ID).Msg("failed to list vendor orders to create their shipments")
		return order.TotalAmount
	}

	var total int64
	for _, vo := range vendorOrders {
		total += vo.SubtotalAmount

		_, feeAmount, err := uc.shipments.CreateShipment(ctx, adapter.CreateShipmentInput{
			VendorOrderID: vo.ID, VendorID: vo.VendorID, BuyerID: order.BuyerID,
			PackageWeightGrams: weightByVendor[vo.VendorID],
			RecipientName:      address.RecipientName, Phone: address.Phone, Province: address.Province,
			District: address.District, Ward: address.Ward, StreetAddress: address.StreetAddress,
		})
		if err != nil {
			uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to create a shipment at checkout; the vendor can create it manually")
			continue
		}
		if err := uc.vendorOrders.SetShippingFee(ctx, vo.ID, feeAmount); err != nil {
			uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to snapshot the shipping fee for a vendor order")
			continue
		}
		total += feeAmount
	}

	if err := uc.orders.UpdateTotalAmount(ctx, order.ID, total); err != nil {
		uc.log.Error().Err(err).Str("order_id", order.ID).Msg("failed to apply shipping fees to the order total")
		return order.TotalAmount
	}
	return total
}

func (uc *OrderUseCase) Cancel(ctx context.Context, buyerID, orderID string) (*domain.Order, error) {
	order, err := uc.findOwnedByBuyer(ctx, buyerID, orderID)
	if err != nil {
		return nil, err
	}

	if !domain.CanTransition(order.Status, domain.StatusCancelled) {
		return nil, apperror.Conflict("This order can no longer be cancelled")
	}

	return uc.cancel(ctx, order, "Cancelled by buyer")
}

func (uc *OrderUseCase) cancel(ctx context.Context, order *domain.Order, reason string) (*domain.Order, error) {
	if err := uc.orders.UpdateStatus(ctx, order.ID, domain.StatusCancelled, &reason); err != nil {
		return nil, apperror.Internal(err)
	}
	if err := uc.inventory.Release(ctx, order.ID); err != nil {
		uc.log.Error().Err(err).Str("order_id", order.ID).Msg("failed to release inventory for a cancelled order")
	}
	uc.cancelShipments(ctx, order.ID)

	order.Status = domain.StatusCancelled
	order.CancellationReason = &reason
	uc.notify(ctx, order.BuyerID, notifyOrderCancelled, order.ID)
	return order, nil
}

// cancelShipments voids every vendor sub-order's shipment for an order
// being cancelled — best-effort, same as inventory release: a briefly
// unreachable Shipment is logged, never propagated, since the order's own
// cancellation is already committed by this point.
func (uc *OrderUseCase) cancelShipments(ctx context.Context, orderID string) {
	vendorOrders, err := uc.vendorOrders.ListByOrderID(ctx, orderID)
	if err != nil {
		uc.log.Error().Err(err).Str("order_id", orderID).Msg("failed to list vendor orders to cancel their shipments")
		return
	}
	for _, vo := range vendorOrders {
		if err := uc.shipments.CancelForVendorOrder(ctx, vo.ID); err != nil {
			uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to cancel shipment for a cancelled vendor order")
		}
	}
}

// MarkPaid is called by Payment once a payment intent has been captured. It
// never decides whether the payment is legitimate — Payment already
// validated the webhook signature and the amount/currency match — Order's
// only job here is validating and applying the resulting lifecycle
// transition, and finalizing the stock hold into a real sale. It is
// idempotent so a retried webhook delivery is a safe no-op.
func (uc *OrderUseCase) MarkPaid(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	if order.Status == domain.StatusPaid {
		return order, nil
	}
	if !domain.CanTransition(order.Status, domain.StatusPaid) {
		return nil, apperror.Conflict("Order cannot be marked paid from status " + string(order.Status))
	}

	if err := uc.orders.UpdateStatus(ctx, orderID, domain.StatusPaid, nil); err != nil {
		return nil, apperror.Internal(err)
	}

	rule, ruleErr := uc.commissionRules.FindCurrent(ctx)
	if ruleErr != nil {
		uc.log.Error().Err(ruleErr).Str("order_id", orderID).Msg("failed to load the current commission rule; vendor orders will be marked paid without a commission snapshot")
	}

	vendorOrders, err := uc.vendorOrders.ListByOrderID(ctx, orderID)
	if err != nil {
		uc.log.Error().Err(err).Str("order_id", orderID).Msg("failed to list vendor orders while marking order paid")
	}
	for _, vo := range vendorOrders {
		if vo.Status != domain.StatusPendingPayment {
			continue
		}
		if err := uc.vendorOrders.UpdateStatus(ctx, vo.ID, domain.StatusPaid); err != nil {
			uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to mark vendor order paid")
			continue
		}
		if rule == nil {
			continue
		}
		commissionAmount, netAmount := domain.ComputeCommission(vo.SubtotalAmount, rule.RateBps)
		if err := uc.vendorOrders.SetCommission(ctx, vo.ID, rule.RateBps, commissionAmount, netAmount); err != nil {
			uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to snapshot commission for a paid vendor order")
		}
	}

	if err := uc.inventory.Commit(ctx, orderID); err != nil {
		uc.log.Error().Err(err).Str("order_id", orderID).Msg("failed to commit inventory reservation after payment")
	}

	order.Status = domain.StatusPaid
	uc.notify(ctx, order.BuyerID, notifyOrderPaid, order.ID)
	return order, nil
}

// MarkPaymentFailed is called by Payment when a payment intent fails. Order
// treats it exactly like any other cancellation — releasing the stock hold —
// since a pending_payment order with no successful payment has nothing left
// to fulfill.
func (uc *OrderUseCase) MarkPaymentFailed(ctx context.Context, orderID, reason string) (*domain.Order, error) {
	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	if order.Status == domain.StatusCancelled {
		return order, nil
	}
	if !domain.CanTransition(order.Status, domain.StatusCancelled) {
		return nil, apperror.Conflict("Order cannot be cancelled from status " + string(order.Status))
	}

	return uc.cancel(ctx, order, reason)
}

// GetForInternal serves Payment's own read of an order (to size a payment
// intent and verify the buyer owns it) without the buyer-ownership check
// findOwnedByBuyer enforces for buyer-facing requests.
func (uc *OrderUseCase) GetForInternal(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	return order, nil
}

// GetVendorOrderForInternal serves Shipment's read of a vendor sub-order
// (to verify the calling vendor owns it and that it's shippable before
// letting them create a shipment manually) without any ownership check of
// its own — the caller decides what to do with the vendor_id and status it
// gets back. It also recomputes the same buyer/destination/package-weight
// figures Checkout used to quote the shipment fee automatically, so
// Shipment's vendor-triggered fallback (when the automatic checkout-time
// call failed) can create a real, correctly-priced shipment rather than a
// dead end.
func (uc *OrderUseCase) GetVendorOrderForInternal(ctx context.Context, vendorOrderID string) (vo *domain.VendorOrder, order *domain.Order, packageWeightGrams int64, err error) {
	vo, err = uc.vendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorOrderNotFound) {
			return nil, nil, 0, apperror.NotFound("Order not found")
		}
		return nil, nil, 0, apperror.Internal(err)
	}
	order, err = uc.orders.FindByID(ctx, vo.OrderID)
	if err != nil {
		return nil, nil, 0, apperror.Internal(err)
	}

	itemsByVendorOrder, err := uc.vendorOrders.ListItemsByVendorOrderIDs(ctx, []string{vo.ID})
	if err != nil {
		uc.log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to load items to recompute package weight")
		return vo, order, 0, nil
	}
	for _, item := range itemsByVendorOrder[vo.ID] {
		product, err := uc.catalog.GetProduct(ctx, item.ProductID)
		if err != nil || product.PackageWeightGrams == nil {
			continue
		}
		packageWeightGrams += *product.PackageWeightGrams * item.Quantity
	}
	return vo, order, packageWeightGrams, nil
}

// vendorAllowedTargets are the only statuses a vendor may move their own
// sub-order into directly. paid/pending_payment/cancelled/refunded are
// reached only through the payment and admin-intervention paths.
var vendorAllowedTargets = map[domain.Status]bool{
	domain.StatusProcessing: true,
	domain.StatusShipped:    true,
	domain.StatusCompleted:  true,
}

// UpdateVendorOrderStatus lets a vendor advance their own slice of a
// multi-vendor order (processing -> shipped -> completed). After applying
// it, the buyer-facing order status is recomputed as the weakest link across
// every vendor's sub-order, so the buyer only sees "shipped" once every
// vendor has actually shipped their part.
func (uc *OrderUseCase) UpdateVendorOrderStatus(ctx context.Context, userID, vendorOrderID string, newStatus domain.Status) (*domain.VendorOrder, error) {
	if !vendorAllowedTargets[newStatus] {
		return nil, apperror.Validation("Vendors can only move an order to processing, shipped or completed")
	}

	vo, err := uc.vendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		if errors.Is(err, repository.ErrVendorOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	if _, err := uc.vendors.GetApprovedVendorID(ctx, userID, vo.VendorID); err != nil {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	if !domain.CanTransition(vo.Status, newStatus) {
		return nil, apperror.Conflict("Cannot move this order from " + string(vo.Status) + " to " + string(newStatus))
	}

	if err := uc.vendorOrders.UpdateStatus(ctx, vo.ID, newStatus); err != nil {
		return nil, apperror.Internal(err)
	}
	vo.Status = newStatus

	if err := uc.recomputeOrderStatus(ctx, vo.OrderID); err != nil {
		uc.log.Error().Err(err).Str("order_id", vo.OrderID).Msg("failed to recompute aggregate order status")
	}

	if notifType, ok := map[domain.Status]string{domain.StatusShipped: notifyOrderShipped, domain.StatusCompleted: notifyOrderCompleted}[newStatus]; ok {
		if parent, err := uc.orders.FindByID(ctx, vo.OrderID); err == nil {
			uc.notify(ctx, parent.BuyerID, notifType, vo.OrderID)
		}
	}
	return vo, nil
}

func (uc *OrderUseCase) recomputeOrderStatus(ctx context.Context, orderID string) error {
	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	// pending_payment/cancelled are reached only outside vendor-driven
	// fulfillment progress; nothing to recompute for either.
	if order.Status == domain.StatusPendingPayment || order.Status == domain.StatusCancelled {
		return nil
	}

	vendorOrders, err := uc.vendorOrders.ListByOrderID(ctx, orderID)
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
	return uc.orders.UpdateStatus(ctx, orderID, target, nil)
}

// adminAllowedTargets are the only interventions an admin can make on an
// order directly. Ordinary fulfillment progress stays vendor-driven;
// admin's role here is moderation (cancelling a stuck order, refunding a
// dispute), not running the happy path.
var adminAllowedTargets = map[domain.Status]bool{
	domain.StatusCancelled: true,
	domain.StatusRefunded:  true,
}

func (uc *OrderUseCase) AdminList(ctx context.Context, status string, limit, offset int) ([]*domain.Order, error) {
	orders, err := uc.orders.ListByStatus(ctx, status, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return orders, nil
}

// AdminTransition applies an admin intervention. Like every other status
// change, it is validated against the same state machine everyone else
// uses — admin does not bypass the rule that a shipped order can't jump
// straight to cancelled, only to refunded.
func (uc *OrderUseCase) AdminTransition(ctx context.Context, orderID string, newStatus domain.Status, reason string) (*domain.Order, error) {
	if !adminAllowedTargets[newStatus] {
		return nil, apperror.Validation("Admin can only cancel or refund an order")
	}
	if reason == "" {
		return nil, apperror.Validation("A reason is required")
	}

	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	if !domain.CanTransition(order.Status, newStatus) {
		return nil, apperror.Conflict("Cannot move this order from " + string(order.Status) + " to " + string(newStatus))
	}

	if err := uc.orders.UpdateStatus(ctx, orderID, newStatus, &reason); err != nil {
		return nil, apperror.Internal(err)
	}
	if newStatus == domain.StatusCancelled {
		if err := uc.inventory.Release(ctx, orderID); err != nil {
			uc.log.Error().Err(err).Str("order_id", orderID).Msg("failed to release inventory for an admin-cancelled order")
		}
		uc.cancelShipments(ctx, orderID)
	}

	order.Status = newStatus
	order.CancellationReason = &reason
	if newStatus == domain.StatusCancelled {
		uc.notify(ctx, order.BuyerID, notifyOrderCancelled, order.ID)
	} else {
		uc.notify(ctx, order.BuyerID, notifyOrderRefunded, order.ID)
	}
	return order, nil
}

func (uc *OrderUseCase) GetOwnedByBuyer(ctx context.Context, buyerID, orderID string) (*domain.Order, []*domain.OrderItem, []*domain.VendorOrder, error) {
	order, err := uc.findOwnedByBuyer(ctx, buyerID, orderID)
	if err != nil {
		return nil, nil, nil, err
	}

	items, err := uc.orders.ListItemsByOrder(ctx, order.ID)
	if err != nil {
		return nil, nil, nil, apperror.Internal(err)
	}
	vendorOrders, err := uc.vendorOrders.ListByOrderID(ctx, order.ID)
	if err != nil {
		return nil, nil, nil, apperror.Internal(err)
	}
	return order, items, vendorOrders, nil
}

func (uc *OrderUseCase) ListMine(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Order, error) {
	orders, err := uc.orders.ListByBuyer(ctx, buyerID, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return orders, nil
}

// ListVendorMine is the vendor's own order list, now including each
// sub-order's items — previously this returned financial totals only,
// leaving a vendor with no way to see what they actually need to fulfill.
func (uc *OrderUseCase) ListVendorMine(ctx context.Context, userID, vendorID string, limit, offset int) ([]*domain.VendorOrder, map[string][]*domain.OrderItem, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, nil, err
	}

	vendorOrders, err := uc.vendorOrders.ListByVendor(ctx, vendorID, limit, offset)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}

	vendorOrderIDs := make([]string, 0, len(vendorOrders))
	for _, vo := range vendorOrders {
		vendorOrderIDs = append(vendorOrderIDs, vo.ID)
	}
	items, err := uc.vendorOrders.ListItemsByVendorOrderIDs(ctx, vendorOrderIDs)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}

	return vendorOrders, items, nil
}

// GetVendorSummary is the vendor dashboard's read model: total orders,
// revenue, commission and net for everything paid or further, plus their
// best-selling products by quantity.
func (uc *OrderUseCase) GetVendorSummary(ctx context.Context, userID, vendorID string) (*domain.VendorSummary, []*domain.TopProduct, error) {
	vendorID, err := uc.vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, nil, err
	}

	summary, err := uc.vendorOrders.SummaryByVendor(ctx, vendorID)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	topProducts, err := uc.vendorOrders.TopProductsByVendor(ctx, vendorID, 5)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	return summary, topProducts, nil
}

// QuantitySoldByProductIDs serves Catalog's storefront listing: how many
// units of each product have sold, across every vendor. Internal/trusted —
// no ownership check, unlike the vendor-scoped methods above.
func (uc *OrderUseCase) QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error) {
	quantities, err := uc.vendorOrders.QuantitySoldByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return quantities, nil
}

// SetCommissionRule is an admin-only action that adds a new versioned
// commission rate. It never edits or removes a past rule — every vendor
// order marked paid before this call keeps the commission it was already
// snapshotted with.
func (uc *OrderUseCase) SetCommissionRule(ctx context.Context, adminUserID string, rateBps int) (*domain.CommissionRule, error) {
	if err := domain.ValidateCommissionRateBps(rateBps); err != nil {
		return nil, err
	}

	rule := &domain.CommissionRule{RateBps: rateBps, CreatedBy: &adminUserID}
	if err := uc.commissionRules.Create(ctx, rule); err != nil {
		return nil, apperror.Internal(err)
	}
	return rule, nil
}

func (uc *OrderUseCase) ListCommissionRules(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error) {
	rules, err := uc.commissionRules.List(ctx, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return rules, nil
}

func (uc *OrderUseCase) findOwnedByBuyer(ctx context.Context, buyerID, orderID string) (*domain.Order, error) {
	order, err := uc.orders.FindByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, apperror.NotFound("Order not found")
		}
		return nil, apperror.Internal(err)
	}
	if order.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	return order, nil
}
