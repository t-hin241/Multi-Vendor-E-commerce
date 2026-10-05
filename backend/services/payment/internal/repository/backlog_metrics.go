package repository

import "shopee/backend/pkg/telemetry"

// Backlogs are this service's outboxes as exported to metrics
// (telemetry.RegisterOutboxes): rows still to deliver and how long the
// oldest one has waited. Parked or review rows are left out: they have
// their own operator views and would otherwise keep the age alert firing.
var Backlogs = []telemetry.Outbox{
	{Name: "payment_order_sync", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM payment_order_sync WHERE delivered_at IS NULL AND NOT requires_review`},
	{Name: "payment_refund_sync", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM payment_refund_sync WHERE delivered_at IS NULL AND NOT requires_review`},
}
