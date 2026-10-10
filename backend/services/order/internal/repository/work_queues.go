package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "order_effects_parked", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM order_effects WHERE status = 'parked'`},
	{Name: "settlement_holds_needs_review", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(t)), 0)::float8 FROM (
		SELECT updated_at AS t FROM support_cases WHERE hold_status = 'needs_review'
		UNION ALL SELECT updated_at FROM cancellation_requests WHERE hold_status = 'needs_review'
		UNION ALL SELECT updated_at FROM delivery_exceptions WHERE hold_status = 'needs_review'
		UNION ALL SELECT updated_at FROM source_settlement_holds WHERE status = 'needs_review') h`},
	{Name: "settlement_holds_preparing_1h", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(t)), 0)::float8 FROM (
		SELECT updated_at AS t FROM support_cases WHERE hold_status = 'preparing'
		UNION ALL SELECT updated_at FROM cancellation_requests WHERE hold_status = 'preparing'
		UNION ALL SELECT updated_at FROM delivery_exceptions WHERE hold_status = 'preparing'
		UNION ALL SELECT updated_at FROM source_settlement_holds WHERE status = 'preparing') h
		WHERE t < now() - interval '1 hour'`},
	{Name: "cancellations_stuck", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM cancellation_requests
		WHERE status = 'needs_review' OR (status IN ('stopping_fulfillment', 'refund_pending') AND updated_at < now() - interval '1 hour')`},
	{Name: "delivery_exceptions_stuck", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM delivery_exceptions
		WHERE status = 'needs_review' OR (status <> 'resolved' AND updated_at < now() - interval '24 hours')`},
	{Name: "return_shipping_review", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM return_requests
		WHERE (status = 'approved' AND (shipping_status = 'destination_missing' OR dispatch_overdue_at IS NOT NULL))
		   OR (status = 'received' AND inspection_disputed)`},
	{Name: "case_sla_overdue", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(due_at)), 0)::float8 FROM case_sla_work_items WHERE active AND due_at < now()`},
	{Name: "buyer_notices_review", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM order_buyer_notices WHERE delivered_at IS NULL AND requires_review`},
}
