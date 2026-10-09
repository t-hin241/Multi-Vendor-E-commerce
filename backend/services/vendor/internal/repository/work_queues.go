package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "staff_invitations_parked", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM staff_invitations WHERE delivery_status = 'parked'`},
	{Name: "return_destinations_unverified_24h", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM vendor_return_destinations WHERE verified_version IS DISTINCT FROM version AND updated_at < now() - interval '24 hours'`},
}
