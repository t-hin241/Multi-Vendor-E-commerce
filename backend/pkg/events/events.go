// Package events is the catalogue of domain events exchanged on the event
// bus (PLT-03/05): each type's name, schema version and payload. Producer
// and consumer both use these definitions, so a payload change is a
// compile error on both sides; contract tests pin the JSON. A breaking
// change gets a new schema version that consumers accept before producers
// send it (pkg/events/CONTRACTS.md).
package events

import (
	"time"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/vendorsales"
)

// Event types (producer.fact).
const (
	VendorStatusChanged         = "vendor.status_changed"
	VendorNotificationRequested = "vendor.notification_requested"
	ProductStatusChanged        = "catalog.product_status_changed"
	ReservationExpired          = "inventory.reservation_expired"
	StockChanged                = "inventory.stock_changed"
	OrderNotificationRequested  = "order.notification_requested"
	FulfillmentReady            = "order.fulfillment_ready"
	FulfillmentCancelled        = "order.fulfillment_cancelled"
	VendorOrderSettleable       = "order.vendor_order_settleable"
	PaymentOutcomeRejected      = "order.payment_outcome_rejected"
	ShipmentChanged             = "shipment.status_changed"
	PaymentOutcome              = "payment.outcome"
	RefundOutcome               = "payment.refund_outcome"
)

// V1 is the schema version of every payload below.
const V1 = 1

// VendorStatus: a shop's selling permission changed (Vendor → Catalog,
// Order). Consumers keep the highest version.
func VendorStatus(eventID string, s vendorsales.Status) (eventbus.Envelope, error) {
	return eventbus.New(eventID, VendorStatusChanged, V1, s.VendorID, s.Version, s)
}

// ProductStatus: a product's storefront visibility changed (Catalog →
// Order). Consumers keep the highest version.
func ProductStatus(eventID string, s productsales.Status) (eventbus.Envelope, error) {
	return eventbus.New(eventID, ProductStatusChanged, V1, s.ProductID, s.Version, s)
}

// NotificationRequest asks Notification to tell a user about a fact
// (Order, Vendor → Notification). It carries references only: Notification
// reads the address from Identity. The event id is the notification's
// event id (dedup); the producer is its source.
type NotificationRequest struct {
	UserID      string `json:"user_id"`
	Type        string `json:"type"`
	ReferenceID string `json:"reference_id"`
}

// VendorNotification: a shop decision to tell its owner about.
func VendorNotification(eventID, vendorID string, n NotificationRequest) (eventbus.Envelope, error) {
	return eventbus.New(eventID, VendorNotificationRequested, V1, vendorID, 0, n)
}

// OrderNotification: an order fact to tell the buyer about.
func OrderNotification(eventID, orderID string, n NotificationRequest) (eventbus.Envelope, error) {
	return eventbus.New(eventID, OrderNotificationRequested, V1, orderID, 0, n)
}

// StockChange: stock of these variants changed (Inventory → Catalog
// storefront cache). Invalidation is idempotent, so a republish under a
// new event id is harmless.
type StockChange struct {
	VariantIDs []string `json:"variant_ids"`
}

func StockChangedEvent(variantIDs []string) (eventbus.Envelope, error) {
	return eventbus.New(eventbus.NewID(), StockChanged, V1, "stock", 0, StockChange{VariantIDs: variantIDs})
}

// ReservationExpiry: an order's stock hold expired (Inventory → Order).
type ReservationExpiry struct {
	ID      string `json:"id"`
	OrderID string `json:"order_id"`
	Type    string `json:"type"`
}

func ReservationExpiredEvent(r ReservationExpiry) (eventbus.Envelope, error) {
	return eventbus.New(r.ID, ReservationExpired, V1, r.OrderID, 0, r)
}

// QuotedFee is the shipping fee the buyer paid, as quoted at checkout.
type QuotedFee struct {
	FeeAmount int64  `json:"fee_amount"`
	CarrierID string `json:"carrier_id"`
	ZoneID    string `json:"zone_id"`
	FeeRuleID string `json:"fee_rule_id"`
}

// Fulfillment: a paid vendor order is ready to ship (Order → Shipment).
// It carries the delivery address only, not the order.
type Fulfillment struct {
	VendorOrderID      string     `json:"vendor_order_id"`
	VendorID           string     `json:"vendor_id"`
	BuyerID            string     `json:"buyer_id"`
	PackageWeightGrams int64      `json:"package_weight_grams"`
	RecipientName      string     `json:"recipient_name"`
	Phone              string     `json:"phone"`
	Province           string     `json:"province"`
	District           string     `json:"district"`
	Ward               string     `json:"ward"`
	StreetAddress      string     `json:"street_address"`
	Quote              *QuotedFee `json:"quote,omitempty"`
}

func FulfillmentReadyEvent(eventID, orderID string, f Fulfillment) (eventbus.Envelope, error) {
	return eventbus.New(eventID, FulfillmentReady, V1, f.VendorOrderID, 0, f)
}

// FulfillmentCancellation: a vendor order was cancelled; its shipment
// must not leave (Order → Shipment).
type FulfillmentCancellation struct {
	VendorOrderID string `json:"vendor_order_id"`
}

func FulfillmentCancelledEvent(eventID string, c FulfillmentCancellation) (eventbus.Envelope, error) {
	return eventbus.New(eventID, FulfillmentCancelled, V1, c.VendorOrderID, 0, c)
}

// Settlement: a completed vendor order with its checkout snapshot, payable
// after the return window (Order → Payment ledger).
type Settlement struct {
	VendorOrderID         string    `json:"vendor_order_id"`
	OrderID               string    `json:"order_id"`
	VendorID              string    `json:"vendor_id"`
	Currency              string    `json:"currency"`
	SubtotalAmount        int64     `json:"subtotal_amount"`
	ShippingAmount        int64     `json:"shipping_amount"`
	CommissionAmount      int64     `json:"commission_amount"`
	CommissionRateBps     int       `json:"commission_rate_bps"`
	CommissionRuleVersion *int64    `json:"commission_rule_version,omitempty"`
	CompletedAt           time.Time `json:"completed_at"`
	EligibleAt            time.Time `json:"eligible_at"`
}

func VendorOrderSettleableEvent(eventID string, s Settlement) (eventbus.Envelope, error) {
	return eventbus.New(eventID, VendorOrderSettleable, V1, s.VendorOrderID, 0, s)
}

// ShipmentFact: a package shipped, was delivered or came back (Shipment →
// Order). Order decides what it means for the vendor order.
type ShipmentFact struct {
	EventID        string    `json:"event_id"`
	ShipmentID     string    `json:"shipment_id"`
	VendorOrderID  string    `json:"vendor_order_id"`
	Type           string    `json:"type"`
	OccurredAt     time.Time `json:"occurred_at"`
	TrackingNumber *string   `json:"tracking_number,omitempty"`
}

func ShipmentChangedEvent(f ShipmentFact) (eventbus.Envelope, error) {
	return eventbus.New(f.EventID, ShipmentChanged, V1, f.VendorOrderID, 0, f)
}

// PaymentResult: a payment for an order was captured or failed (Payment
// → Order). A capture carries what Order checks against its snapshot.
type PaymentResult struct {
	PaymentID string `json:"payment_id"`
	OrderID   string `json:"order_id"`
	Outcome   string `json:"outcome"` // captured | failed
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reason    string `json:"reason,omitempty"`
}

// PaymentOutcomeEvent: one event per payment intent and outcome.
func PaymentOutcomeEvent(p PaymentResult) (eventbus.Envelope, error) {
	return eventbus.New("payment-"+p.PaymentID+"-"+p.Outcome, PaymentOutcome, V1, p.OrderID, 0, p)
}

// RefundResult: a refund Payment confirmed or that failed (Payment →
// Order).
type RefundResult struct {
	OrderRefundID   string `json:"order_refund_id"`
	PaymentRefundID string `json:"payment_refund_id"`
	Status          string `json:"status"` // succeeded | failed
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	FailureReason   string `json:"failure_reason,omitempty"`
}

func RefundOutcomeEvent(r RefundResult) (eventbus.Envelope, error) {
	return eventbus.New("refund-"+r.PaymentRefundID+"-"+r.Status, RefundOutcome, V1, r.OrderRefundID, 0, r)
}

// OutcomeRejection: Order refused a payment or refund outcome (amount
// mismatch, order already cancelled, contradicting result); Payment puts it
// up for review (Order → Payment).
type OutcomeRejection struct {
	Kind            string `json:"kind"` // payment | refund
	PaymentID       string `json:"payment_id,omitempty"`
	PaymentRefundID string `json:"payment_refund_id,omitempty"`
	OrderID         string `json:"order_id"`
	Reason          string `json:"reason"`
}

func OutcomeRejectedEvent(eventID string, r OutcomeRejection) (eventbus.Envelope, error) {
	return eventbus.New(eventID, PaymentOutcomeRejected, V1, r.OrderID, 0, r)
}
