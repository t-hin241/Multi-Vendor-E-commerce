package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Operations counts the order work an operator should look at, for the
// admin operations dashboard.
type Operations struct{ Pool *pgxpool.Pool }

// Counts: vendor orders paid but not shipped (and those older than three
// days), captures Order rejected with no refund yet, refunds Payment has
// not settled, returns whose refund failed, and support cases waiting for
// the marketplace (unassigned, past their deadline, waiting for a refund
// or return outcome).
func (r Operations) Counts(ctx context.Context) (map[string]int64, error) {
	var awaiting, late, exceptions, refundsOpen, returnRefundsFailed, casesUnassigned, casesOverdue, casesPending int64
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT
		(SELECT count(*) FROM vendor_orders WHERE status IN ('paid', 'processing')),
		(SELECT count(*) FROM vendor_orders WHERE status IN ('paid', 'processing') AND updated_at < now() - interval '3 days'),
		(SELECT count(*) FROM order_payments p WHERE p.outcome = 'rejected' AND NOT EXISTS (
			SELECT 1 FROM order_refunds f WHERE f.payment_id = p.payment_id AND f.status IN ('requested', 'submitted', 'succeeded'))),
		(SELECT count(*) FROM order_refunds WHERE status IN ('requested', 'submitted')),
		(SELECT count(*) FROM return_requests WHERE status = 'refund_failed'),
		(SELECT count(*) FROM case_sla_work_items WHERE resource_type='support' AND active AND payload->>'assignee_id' IS NULL),
		(SELECT count(*) FROM case_sla_work_items WHERE resource_type='support' AND active AND due_at <= now()),
		(SELECT count(*) FROM support_cases WHERE status = 'resolution_pending')`).
		Scan(&awaiting, &late, &exceptions, &refundsOpen, &returnRefundsFailed, &casesUnassigned, &casesOverdue, &casesPending)
	return map[string]int64{
		"awaiting_shipment": awaiting, "awaiting_shipment_over_3_days": late, "payment_exceptions": exceptions,
		"refunds_open": refundsOpen, "return_refunds_failed": returnRefundsFailed,
		"support_cases_unassigned": casesUnassigned, "support_cases_overdue": casesOverdue, "support_cases_resolution_pending": casesPending,
	}, err
}
