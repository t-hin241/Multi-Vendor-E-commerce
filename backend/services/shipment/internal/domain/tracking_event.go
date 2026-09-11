package domain

import "time"

// TrackingEvent is an automatic audit-on-mutation record of a shipment's
// status changes — written as a side effect of Advance, never authored
// directly, mirroring inventory's stock_movements pattern. This is what
// gives buyers and vendors an actual timeline to look at instead of just
// the shipment's current status.
type TrackingEvent struct {
	ID         string
	ShipmentID string
	Status     Status
	Note       *string
	CreatedAt  time.Time
}
