package repository

import "shopee/backend/pkg/telemetry"

// Backlogs are this service's outboxes as exported to metrics
// (telemetry.RegisterOutboxes): rows still to deliver and how long the
// oldest one has waited. Parked or review rows are left out: they have
// their own operator views and would otherwise keep the age alert firing.
var Backlogs = []telemetry.Outbox{
	{Name: "inventory_outbox", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM inventory_outbox WHERE delivered_at IS NULL AND attempts < 10`},
	// No created_at column: the age is how long the most overdue row has been due.
	{Name: "inventory_stock_outbox", SQL: `SELECT count(*) FILTER (WHERE attempts < 10), COALESCE(EXTRACT(EPOCH FROM now() - min(next_attempt_at) FILTER (WHERE attempts < 10 AND next_attempt_at <= now())), 0)::float8 FROM inventory_stock_outbox`},
}
