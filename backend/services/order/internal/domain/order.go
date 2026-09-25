// Package domain holds Order's entities and business rules: the order
// status state machine, and the pricing-snapshot invariant that an item's
// price is fixed at checkout and never recalculated from a later price.
package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	StatusPendingPayment Status = "pending_payment"
	StatusPaid           Status = "paid"
	StatusProcessing     Status = "processing"
	StatusShipped        Status = "shipped"
	StatusCompleted      Status = "completed"
	StatusCancelled      Status = "cancelled"
	StatusRefunded       Status = "refunded"
)

// validTransitions is the only place an order's lifecycle is defined. It
// enforces, among other things, that an order can never be shipped before
// it's paid, and that cancelled/refunded are terminal.
var validTransitions = map[Status][]Status{
	StatusPendingPayment: {StatusPaid, StatusCancelled},
	StatusPaid:           {StatusProcessing, StatusRefunded},
	StatusProcessing:     {StatusShipped, StatusRefunded},
	StatusShipped:        {StatusCompleted, StatusRefunded},
	StatusCompleted:      {StatusRefunded},
	StatusCancelled:      {},
	StatusRefunded:       {},
}

func CanTransition(from, to Status) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// progressRank orders the non-terminal, post-payment statuses from least to
// most complete. It excludes pending_payment (vendor orders never sit there
// once the order is paid) and cancelled/refunded, which AggregateStatus
// handles separately.
var progressRank = []Status{StatusPaid, StatusProcessing, StatusShipped, StatusCompleted}

func rankOf(s Status) int {
	for i, r := range progressRank {
		if r == s {
			return i
		}
	}
	return 0
}

// AggregateStatus derives a multi-vendor order's overall status from its
// vendor sub-orders' statuses: the buyer sees "shipped" only once every
// vendor has shipped their part, and so on. A sub-order that has been
// refunded is excluded from that weakest-link comparison — it no longer
// gates the buyer's view of the rest of the order — unless every sub-order
// has been refunded, in which case the whole order is refunded.
func AggregateStatus(vendorOrderStatuses []Status) Status {
	if len(vendorOrderStatuses) == 0 {
		return StatusPendingPayment
	}

	weakest := progressRank[len(progressRank)-1]
	weakestRank := len(progressRank) - 1
	sawActive := false

	for _, s := range vendorOrderStatuses {
		if s == StatusRefunded {
			continue
		}
		sawActive = true
		if r := rankOf(s); r < weakestRank {
			weakestRank = r
			weakest = progressRank[r]
		}
	}

	if !sawActive {
		return StatusRefunded
	}
	return weakest
}

// RecipientName through StreetAddress are a snapshot of the buyer's chosen
// address taken once at checkout, never re-derived from a later address
// book edit — same "snapshot at the boundary" convention as
// OrderItem.ProductName.
type Order struct {
	ID                 string
	BuyerID            string
	Status             Status
	TotalAmount        int64
	Currency           string
	CancellationReason *string
	RecipientName      string
	Phone              string
	Province           string
	District           string
	Ward               string
	StreetAddress      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CommissionRateBps/CommissionAmount/NetAmount are nil until the vendor
// order is marked paid, at which point they're snapshotted once from
// whatever CommissionRule was current — never recomputed later from a
// changed rule. ShippingFeeAmount starts at 0 and is set once, right after
// checkout, by a follow-up update once Shipment has quoted the fee — see
// OrderUseCase.Checkout.
type VendorOrder struct {
	ID                string
	OrderID           string
	VendorID          string
	Status            Status
	SubtotalAmount    int64
	ShippingFeeAmount int64
	Currency          string
	CommissionRateBps *int
	CommissionAmount  *int64
	NetAmount         *int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CommissionRule is one versioned commission percentage, expressed in basis
// points (1/100 of a percent; 1000 = 10.00%) to avoid float math on money.
// Rules are insert-only — setting a new one never edits an existing row, so
// a past vendor order's snapshot is never retroactively changed.
type CommissionRule struct {
	ID        string
	RateBps   int
	CreatedBy *string
	CreatedAt time.Time
}

const maxCommissionRateBps = 10000 // 100.00%

func ValidateCommissionRateBps(rateBps int) error {
	if rateBps < 0 || rateBps > maxCommissionRateBps {
		return apperror.Validation("Commission rate must be between 0 and 10000 basis points (0-100%)")
	}
	return nil
}

// VendorSummary aggregates a vendor's paid-or-further vendor orders for
// their dashboard: how much they've sold, how much the marketplace took in
// commission, and what nets out to them.
type VendorSummary struct {
	TotalOrders     int64
	TotalRevenue    int64
	TotalCommission int64
	TotalNet        int64
}

// TopProduct is one line of a vendor's best-sellers, computed from order
// items across their paid-or-further vendor orders.
type TopProduct struct {
	ProductID     string
	ProductName   string
	QuantitySold  int64
	RevenueAmount int64
}

// ComputeCommission splits a vendor order's subtotal into what the
// marketplace keeps and what the vendor nets, using integer basis-point
// math so no fractional currency unit is ever introduced by float rounding.
func ComputeCommission(subtotalAmount int64, rateBps int) (commissionAmount, netAmount int64) {
	commissionAmount = subtotalAmount * int64(rateBps) / maxCommissionRateBps
	netAmount = subtotalAmount - commissionAmount
	return commissionAmount, netAmount
}

// OrderItem's PriceAmount and SubtotalAmount are a snapshot taken at
// checkout — they are never recalculated from Catalog's current price after
// the order exists.
type OrderItem struct {
	ID             string
	OrderID        string
	VendorOrderID  string
	ProductID      string
	ProductName    string
	VariantID      *string
	VariantSKU     *string
	VariantLabel   *string
	PriceAmount    int64
	Quantity       int64
	SubtotalAmount int64
	CreatedAt      time.Time
}

// CheckoutLine is one line of a checkout request after Order has resolved
// it against Catalog: an authoritative price snapshot plus which vendor it
// belongs to, ready to be grouped into vendor sub-orders. VariantSKU/Label
// are nil unless VariantID is set — a variant has no price of its own, so
// pricing here is always the parent product's.
type CheckoutLine struct {
	ProductID    string
	VendorID     string
	ProductName  string
	VariantID    *string
	VariantSKU   *string
	VariantLabel *string
	PriceAmount  int64
	Currency     string
	Quantity     int64
}

func (l CheckoutLine) Subtotal() int64 {
	return l.PriceAmount * l.Quantity
}
