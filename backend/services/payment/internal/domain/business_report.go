package domain

import (
	"time"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/pkg/vendorreport"
)

// BusinessReport is Payment's part of the AF-20 report (definition v1).
// Money is what Payment recorded, by the time it recorded it:
//   - marketplace scope: captured = captures recorded in the period
//     (captured_at), refunded = refunds confirmed in the period, net_cash =
//     captured - refunded. Not comparable to Order's gmv_ordered.
//   - shop scope: a capture pays for every shop of an order, so a shop's
//     captured share is unknown (never estimated); refunded, settled sales
//     and paid out come from the shop's settlement ledger by recorded time,
//     the same entries as its statement (AF-14).
type BusinessReport struct {
	Source            string                `json:"source"`
	DefinitionVersion string                `json:"definition_version"`
	Range             bizreport.Range       `json:"range"`
	AsOf              time.Time             `json:"as_of"`
	Currency          string                `json:"currency"`
	Scope             string                `json:"scope"` // marketplace | shop
	Captured          vendorreport.Amount   `json:"captured"`
	Refunded          vendorreport.Amount   `json:"refunded"`
	NetCash           vendorreport.Amount   `json:"net_cash"`
	SettledSales      vendorreport.Amount   `json:"settled_sales"`
	PaidOut           vendorreport.Amount   `json:"paid_out"`
	CapturedCount     *int64                `json:"captured_count,omitempty"`
	RefundCount       int64                 `json:"refund_count"`
	EstimatedCaptures int64                 `json:"estimated_captures"`
	Series            []PaymentReportBucket `json:"series"`
}

// PaymentReportBucket is one day or month (Vietnam time). Captured is null
// in shop scope.
type PaymentReportBucket struct {
	Start    time.Time `json:"bucket_start"`
	Captured *int64    `json:"captured"`
	Refunded int64     `json:"refunded"`
}

// PaymentReportRow is one money record behind a figure (no account data).
type PaymentReportRow struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"` // payment | refund | ledger_refund
	OrderID       string    `json:"order_id,omitempty"`
	VendorOrderID string    `json:"vendor_order_id,omitempty"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	RecordedAt    time.Time `json:"recorded_at"`
	Estimated     bool      `json:"estimated,omitempty"`
}
