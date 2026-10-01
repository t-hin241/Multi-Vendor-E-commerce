package domain

// Audit entity types (shipment_admin_audit.entity_type).
const (
	AuditShipment   = "shipment"
	AuditCarrier    = "carrier"
	AuditZone       = "zone"
	AuditFeeRule    = "fee_rule"
	AuditOrderEvent = "order_event"
)

// AdminAction is one admin decision recorded in the same transaction as
// the change it made. Changes keeps only the changed fields, never the
// buyer's contact details.
type AdminAction struct {
	ActorID    string
	Action     string
	EntityType string
	EntityID   string
	Reason     *string
	Changes    map[string]any
}

// Change is a from/to pair for AdminAction.Changes.
func Change(from, to any) []any { return []any{from, to} }
