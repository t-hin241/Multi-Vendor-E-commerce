package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "vendor_actions_need_review", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM vendor_action_events WHERE status IN ('no_recipient', 'parked')`},
	{Name: "notifications_parked", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM notifications WHERE status = 'parked'`},
}
