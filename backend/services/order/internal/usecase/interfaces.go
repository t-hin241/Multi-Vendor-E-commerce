package usecase

import (
	"context"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
)

type OrderRepositoryPort interface {
	CreateFromPlan(ctx context.Context, plan *domain.Plan) (*domain.Order, error)
	FindByID(ctx context.Context, id string) (*domain.Order, error)
	ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Order, error)
	ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Order, error)
	UpdateStatus(ctx context.Context, id string, status domain.Status, reason *string) error
	// UpdateTotalAmount is a follow-up update once Shipment has quoted every
	// vendor sub-order's fee at checkout time.
	UpdateTotalAmount(ctx context.Context, id string, totalAmount int64) error
	ListItemsByOrder(ctx context.Context, orderID string) ([]*domain.OrderItem, error)
}

type VendorOrderRepositoryPort interface {
	FindByID(ctx context.Context, id string) (*domain.VendorOrder, error)
	ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.VendorOrder, error)
	ListByOrderID(ctx context.Context, orderID string) ([]*domain.VendorOrder, error)
	UpdateStatus(ctx context.Context, id string, status domain.Status) error
	SetCommission(ctx context.Context, id string, rateBps int, commissionAmount, netAmount int64) error
	// SetShippingFee is a follow-up snapshot, same shape as SetCommission,
	// applied once Shipment has quoted the fee at checkout time.
	SetShippingFee(ctx context.Context, id string, feeAmount int64) error
	SummaryByVendor(ctx context.Context, vendorID string) (*domain.VendorSummary, error)
	TopProductsByVendor(ctx context.Context, vendorID string, limit int) ([]*domain.TopProduct, error)
	QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error)
	// ListItemsByVendorOrderIDs batch-looks-up order items scoped to
	// several vendor orders at once, keyed by vendor_order_id — backs the
	// vendor's own order list/export, which previously showed no item
	// detail at all.
	ListItemsByVendorOrderIDs(ctx context.Context, vendorOrderIDs []string) (map[string][]*domain.OrderItem, error)
}

// BuyerAddressRepositoryPort is a buyer's own saved shipping address book —
// mirrors Vendor's warehouse-address shape.
type BuyerAddressRepositoryPort interface {
	Create(ctx context.Context, a *domain.BuyerAddress) error
	FindByID(ctx context.Context, id string) (*domain.BuyerAddress, error)
	ListForBuyer(ctx context.Context, buyerID string) ([]*domain.BuyerAddress, error)
	FindDefaultForBuyer(ctx context.Context, buyerID string) (*domain.BuyerAddress, error)
	Update(ctx context.Context, id string, a *domain.BuyerAddress) error
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, buyerID, addressID string) error
}

// CommissionRuleRepositoryPort is Order's own commission-rule ledger.
// Setting a new rule (admin-only) is always an insert; nothing ever edits a
// past rule, since a vendor order's commission is snapshotted at payment
// time from whatever rule was current then.
type CommissionRuleRepositoryPort interface {
	Create(ctx context.Context, rule *domain.CommissionRule) error
	FindCurrent(ctx context.Context) (*domain.CommissionRule, error)
	List(ctx context.Context, limit, offset int) ([]*domain.CommissionRule, error)
}

// CartGateway lets the use case read a buyer's cart selection and clear it
// after checkout, without owning any cart data itself.
type CartGateway interface {
	GetItems(ctx context.Context, bearerToken string) ([]adapter.CartLine, error)
	Clear(ctx context.Context, bearerToken string) error
}

// CatalogGateway is the checkout pricing snapshot's source of truth.
type CatalogGateway interface {
	GetProduct(ctx context.Context, productID string) (*adapter.ProductInfo, error)
	GetVariant(ctx context.Context, variantID string) (*adapter.VariantInfo, error)
}

// VendorGateway lets a vendor user see their own sub-orders.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error)
}

// InventoryGateway is the reserve/release/commit contract used at checkout,
// on cancellation or payment failure, and on payment success respectively.
type InventoryGateway interface {
	Reserve(ctx context.Context, orderID string, lines []adapter.ReserveLine) error
	Release(ctx context.Context, orderID string) error
	Commit(ctx context.Context, orderID string) error
}

// NotificationGateway lets Order tell a buyer about an order event without
// owning any notification data itself. Every call is best-effort from
// Order's side — see OrderUseCase.notify.
type NotificationGateway interface {
	Notify(ctx context.Context, userID, notifType, referenceID string) error
}

// ShipmentGateway is Order's new dependency on Shipment — the reverse
// direction of the pre-existing Shipment -> Order read. Every call is
// best-effort from Order's side (see OrderUseCase.Checkout/cancel): a
// briefly unreachable Shipment must never block or roll back an otherwise-
// valid checkout or cancellation.
type ShipmentGateway interface {
	CreateShipment(ctx context.Context, in adapter.CreateShipmentInput) (shipmentID string, feeAmount int64, err error)
	CancelForVendorOrder(ctx context.Context, vendorOrderID string) error
}
