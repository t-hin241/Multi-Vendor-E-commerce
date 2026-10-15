package domain

import (
	"time"

	"shopee/backend/pkg/bizreport"
)

// LateHandoff is how long a fulfillable package may wait for the carrier
// before it counts as late (the admin dashboard's "3+ days").
const LateHandoff = 72 * time.Hour

// BusinessReport is Shipment's part of the AF-20 report (definition v1).
// The cohort is first delivery attempts opened in the period: a shipment
// opens when Order says the package is fulfillable (capture verified, stock
// committed), so delay runs from that moment to handoff, never from an
// unpaid order. Facts are as of as_of; replacement attempts are left out.
// late_handoff is over packages not cancelled: handed off after
// LateHandoff, or still waiting longer than that at as_of.
type BusinessReport struct {
	Source            string                 `json:"source"`
	DefinitionVersion string                 `json:"definition_version"`
	Range             bizreport.Range        `json:"range"`
	AsOf              time.Time              `json:"as_of"`
	Fulfillable       int64                  `json:"fulfillable"`
	HandedOff         int64                  `json:"handed_off"`
	Delivered         int64                  `json:"delivered"`
	Returned          int64                  `json:"returned"`
	Lost              int64                  `json:"lost"`
	Cancelled         int64                  `json:"cancelled"`
	LateHandoff       bizreport.Ratio        `json:"late_handoff"`
	HandoffHours      *HoursSummary          `json:"handoff_hours"`
	DeliveryHours     *HoursSummary          `json:"delivery_hours"`
	Series            []ShipmentReportBucket `json:"series"`
}

// HoursSummary is null when nothing was measured.
type HoursSummary struct {
	Average float64 `json:"average"`
	P90     float64 `json:"p90"`
	Count   int64   `json:"count"`
}

type ShipmentReportBucket struct {
	Start       time.Time `json:"bucket_start"`
	Fulfillable int64     `json:"fulfillable"`
	HandedOff   int64     `json:"handed_off"`
	Delivered   int64     `json:"delivered"`
}

// ShipmentReportRow is one package behind a figure (no address data).
type ShipmentReportRow struct {
	ID            string     `json:"id"`
	VendorOrderID string     `json:"vendor_order_id"`
	VendorID      string     `json:"vendor_id"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ShippedAt     *time.Time `json:"shipped_at,omitempty"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}
