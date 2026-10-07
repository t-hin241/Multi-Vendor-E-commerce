package repository

import "shopee/backend/pkg/telemetry"

// Backlogs are this service's outboxes as exported to metrics
// (telemetry.RegisterOutboxes): rows still to deliver and how long the
// oldest one has waited. Parked or review rows are left out: they have
// their own operator views and would otherwise keep the age alert firing.
var Backlogs = []telemetry.Outbox{
	{Name: "vendor_outbox", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM vendor_outbox WHERE delivered_at IS NULL AND attempts < 10`},
	{Name: "policy_outbox", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM policy_outbox WHERE delivered_at IS NULL AND attempts < 50`},
	{Name: "vendor_notification_outbox", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM vendor_notification_outbox WHERE delivered_at IS NULL AND parked_at IS NULL`},
}
