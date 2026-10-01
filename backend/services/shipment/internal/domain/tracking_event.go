package domain

import "time"

// TrackingEvent is one entry of a shipment's timeline, written in the same
// transaction as the change it describes, with who made it. EventKey, when
// set, makes a carrier delivery or a retried action land only once.
type TrackingEvent struct {
	ID         string
	ShipmentID string
	Status     Status
	Note       *string
	ActorID    *string
	ActorRole  ActorRole
	EventKey   *string
	OccurredAt time.Time
	CreatedAt  time.Time
}
