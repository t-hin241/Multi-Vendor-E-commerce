// Package usecase orchestrates Order's workflows: checkout (the saga that
// spans Cart, Catalog, Shipment and Inventory), payment outcomes, order and
// vendor-order transitions, refunds and returns, the durable side effects
// they cause, and the buyer/vendor/admin read models.
package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// Deps are OrderUseCase's collaborators. Repositories own Order's data;
// gateways reach other services over their contracts.
type Deps struct {
	Orders          OrderRepositoryPort
	VendorOrders    VendorOrderRepositoryPort
	BuyerAddresses  BuyerAddressRepositoryPort
	CommissionRules CommissionRuleRepositoryPort
	CartConsumption CartConsumptionRepositoryPort
	CheckoutOps     CheckoutOperationRepositoryPort
	Payments        PaymentRecordRepositoryPort
	Effects         EffectRepositoryPort
	Refunds         RefundRepositoryPort
	Returns         ReturnRepositoryPort

	Cart          CartGateway
	Catalog       CatalogGateway
	Vendors       VendorGateway
	Inventory     InventoryGateway
	Shipments     ShipmentGateway
	Notifications NotificationGateway
	Payment       PaymentGateway
	Identity      IdentityGateway
	Audit         AuditRepositoryPort
	Tx            TransactionRunner
	Operations    OperationsReader
	// Events publishes the effects that are events (notification,
	// fulfillment, settlement, rejected outcomes) to the event bus; nil
	// keeps the direct calls (EVENT_PUBLISHING=http, rollback only).
	Events EventPublisher

	ReturnPolicy domain.ReturnPolicy
	Log          zerolog.Logger
	Now          func() time.Time
}

type OrderUseCase struct {
	Deps
}

func NewOrderUseCase(d Deps) *OrderUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.ReturnPolicy.WindowDays <= 0 {
		d.ReturnPolicy = domain.ReturnPolicy{Version: "window-7d", WindowDays: 7}
	}
	return &OrderUseCase{Deps: d}
}

// Notification event types, matching Notification's own vocabulary. The
// contract between the services is the plain string.
const (
	notifyOrderPaid      = "order_paid"
	notifyOrderShipped   = "order_shipped"
	notifyOrderCompleted = "order_completed"
	notifyOrderCancelled = "order_cancelled"
	notifyOrderRefunded  = "order_refunded"
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

// appError keeps business errors and wraps anything else as internal.
func appError(err error) *apperror.Error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return app
	}
	if errors.Is(err, repository.ErrStaleState) {
		return apperror.Conflict("This order changed while it was being updated; please retry")
	}
	return apperror.Internal(err)
}

// asError converts to the error interface without a typed-nil pitfall.
func asError(err error) error {
	if app := appError(err); app != nil {
		return app
	}
	return nil
}

func notFoundOrInternal(err error, notFound error, message string) error {
	if errors.Is(err, notFound) {
		return apperror.NotFound(message)
	}
	return appError(err)
}

func (uc *OrderUseCase) findOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	order, err := uc.Orders.FindByID(ctx, orderID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrOrderNotFound, "Order not found")
	}
	return order, nil
}

func (uc *OrderUseCase) findOwnedByBuyer(ctx context.Context, buyerID, orderID string) (*domain.Order, error) {
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	return order, nil
}

// withOrder runs fn holding the order lock and normalizes its error.
func (uc *OrderUseCase) withOrder(ctx context.Context, orderID string, fn func(context.Context) error) error {
	return asError(uc.Orders.WithLockedOrder(ctx, orderID, fn))
}

// requireAdmin re-verifies an admin with Identity before a sensitive
// action, instead of trusting the access-token role alone. Without Identity
// it refuses: an admin action is never allowed unverified.
func (uc *OrderUseCase) requireAdmin(ctx context.Context, userID string) error {
	if uc.Identity == nil {
		return apperror.Internal(errors.New("admin verification is not configured"))
	}
	if err := uc.Identity.RequireRole(ctx, userID, "admin"); err != nil {
		return asError(err)
	}
	return nil
}

// audit records an admin action in the caller's transaction; without an
// audit store the action fails rather than going unrecorded.
func (uc *OrderUseCase) audit(ctx context.Context, a domain.AdminAction) error {
	if uc.Audit == nil {
		return apperror.Internal(errors.New("admin audit is not configured"))
	}
	return uc.Audit.Record(ctx, a)
}

// adminReason validates the reason an admin gives for an action.
func adminReason(reason string) (*string, error) {
	return domain.ValidateNote(reason, 500, true, "A reason")
}

func ptr[T any](v T) *T { return &v }
