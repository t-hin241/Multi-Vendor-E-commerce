package repository

import "shopee/backend/pkg/telemetry"

// WorkQueues are this service's operator queues exported as metrics (PW-008,
// telemetry.RegisterWorkQueues): each counts only items already past their
// own threshold, so any non-zero value needs a person (deploy/observability/alerts.yml).
var WorkQueues = []telemetry.WorkQueue{
	{Name: "manual_refund_unknown", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8 FROM manual_refund_attempts WHERE stage = 'unknown'`},
	{Name: "manual_refund_submitted_24h", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(submitted_at)), 0)::float8 FROM manual_refund_attempts WHERE stage = 'submitted' AND submitted_at < now() - interval '24 hours'`},
	{Name: "refund_destination_unverified_24h", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(submitted_at)), 0)::float8 FROM refund_destinations WHERE status = 'pending_verification' AND submitted_at < now() - interval '24 hours'`},
	{Name: "settlement_holds_payout_claimed", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(acquired_at)), 0)::float8 FROM settlement_holds WHERE status = 'active' AND payout_claimed`},
	{Name: "approvals_expiring", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM approval_requests WHERE status = 'pending' AND expires_at < now() + interval '1 hour'`},
	{Name: "outcome_sync_review", Severity: "critical", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(t)), 0)::float8 FROM (
		SELECT created_at AS t FROM payment_order_sync WHERE delivered_at IS NULL AND requires_review
		UNION ALL SELECT created_at FROM payment_refund_sync WHERE delivered_at IS NULL AND requires_review) s`},
	{Name: "vendor_notices_review", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM payment_vendor_notices WHERE delivered_at IS NULL AND requires_review`},
	{Name: "buyer_notices_review", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8 FROM payment_buyer_notices WHERE delivered_at IS NULL AND requires_review`},
	{Name: "case_sla_overdue", Severity: "warning", SQL: `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(due_at)), 0)::float8 FROM case_sla_work_items WHERE active AND due_at < now()`},
}
