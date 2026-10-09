package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "return_shipments_stale", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(dispatched_at)), 0)::float8 FROM return_shipments WHERE status = 'in_transit' AND dispatched_at < now() - interval '10 days'`},
	{Name: "case_sla_overdue", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(due_at)), 0)::float8 FROM case_sla_work_items WHERE active AND due_at < now()`},
}
