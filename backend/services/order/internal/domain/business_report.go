package domain

import (
	"time"

	"shopee/backend/pkg/bizreport"
)

// BusinessReport is Order's part of the AF-20 report (definition v1):
//   - placed_orders: parent orders whose checkout finished (accepted),
//     by created_at; vendor_orders: their per-shop sub-orders, counted
//     separately.
//   - gmv_ordered: merchandise subtotal snapshot of those sub-orders before
//     any discount, including unpaid and cancelled ones. Not money received:
//     never add it to Payment's captured.
//   - rates are over the same cohort, with status as of as_of:
//     cancellation = cancelled / vendor_orders, paid_cancellation =
//     cancelled after payment / paid, returns = paid sub-orders with a
//     return request not rejected / paid.
type BusinessReport struct {
	Source              string                `json:"source"`
	DefinitionVersion   string                `json:"definition_version"`
	Range               bizreport.Range       `json:"range"`
	AsOf                time.Time             `json:"as_of"`
	Currency            string                `json:"currency"`
	PlacedOrders        int64                 `json:"placed_orders"`
	VendorOrders        int64                 `json:"vendor_orders"`
	GMVOrdered          int64                 `json:"gmv_ordered"`
	PaidVendorOrders    int64                 `json:"paid_vendor_orders"`
	AwaitingFulfillment int64                 `json:"awaiting_fulfillment"`
	Cancellation        bizreport.Ratio       `json:"cancellation"`
	PaidCancellation    bizreport.Ratio       `json:"paid_cancellation"`
	Returns             bizreport.Ratio       `json:"returns"`
	Series              []OrderReportBucket   `json:"series"`
	Shops               []ShopOrderReportLine `json:"shops,omitempty"`
}

// OrderReportBucket is one day or month (Vietnam time) of the cohort.
type OrderReportBucket struct {
	Start        time.Time `json:"bucket_start"`
	PlacedOrders int64     `json:"placed_orders"`
	VendorOrders int64     `json:"vendor_orders"`
	GMVOrdered   int64     `json:"gmv_ordered"`
}

// ShopOrderReportLine ranks a shop in the marketplace view. Below
// bizreport.MinSample vendor orders its rates are withheld
// (insufficient_sample), never used to penalize the seller.
type ShopOrderReportLine struct {
	VendorID           string          `json:"vendor_id"`
	VendorOrders       int64           `json:"vendor_orders"`
	GMVOrdered         int64           `json:"gmv_ordered"`
	Cancellation       bizreport.Ratio `json:"cancellation"`
	Returns            bizreport.Ratio `json:"returns"`
	InsufficientSample bool            `json:"insufficient_sample"`
}

// OrderReportRow is one id behind a figure (no buyer data).
type OrderReportRow struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"` // order | vendor_order
	OrderID        string    `json:"order_id"`
	VendorID       string    `json:"vendor_id,omitempty"`
	Status         string    `json:"status"`
	SubtotalAmount int64     `json:"subtotal_amount"`
	Currency       string    `json:"currency"`
	Paid           bool      `json:"paid"`
	CreatedAt      time.Time `json:"created_at"`
}
