package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "staff_invitations_parked", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM staff_invitations WHERE delivery_status = 'parked'`},
	{Name: "return_destinations_unverified_24h", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM vendor_return_destinations WHERE verified_version IS DISTINCT FROM version AND updated_at < now() - interval '24 hours'`},
	// AF-18: a vacation start or end the scheduler has not applied a minute
	// after it was due (the shop still sells, or stays closed, past its plan).
	{Name: "shop_vacation_transitions_overdue", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(due)), 0)::float8 FROM (
		SELECT starts_at AS due FROM vendor_availability_intervals WHERE status = 'scheduled' AND starts_at < now() - interval '1 minute'
		UNION ALL SELECT ends_at FROM vendor_availability_intervals WHERE status = 'active' AND ends_at < now() - interval '1 minute') d`},
}
