package domain

// Audit entity types (order_admin_audit.entity_type).
const (
	AuditOrder          = "order"
	AuditRefund         = "refund"
	AuditReturn         = "return_request"
	AuditEffect         = "effect"
	AuditCommissionRule = "commission_rule"
	AuditSupportCase    = "support_case"
)

// AdminAction is one admin decision recorded in the same transaction as
// the change it made. Changes keeps only the fields the action changed
// (from/to pairs or the requested amounts), never contact details.
type AdminAction struct {
	ActorID    string
	Action     string
	EntityType string
	EntityID   string
	OrderID    *string
	Reason     *string
	Changes    map[string]any
}

// Change is a from/to pair for AdminAction.Changes.
func Change(from, to any) []any { return []any{from, to} }
