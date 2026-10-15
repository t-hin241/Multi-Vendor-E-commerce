package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/order/internal/domain"
)

// BusinessReports answers AF-20 from Order's own tables, in one read-only
// REPEATABLE READ transaction so every figure (and as_of) comes from the
// same snapshot. The cohort is sub-orders of finished checkouts created in
// the period, at or before the snapshot time; v1 reports VND only.
type BusinessReports struct{ Pool *pgxpool.Pool }

// orderCohort selects the cohort: $1 from, $2 to, $3 shop (empty = all),
// $4 cutoff. paid: the parent order captured (paid_at), or a sub-order that
// went past payment before paid_at existed.
const orderCohort = `WITH vo AS (
	SELECT v.id, v.order_id, v.vendor_id, v.status, v.subtotal_amount, v.currency, v.created_at,
		(o.paid_at IS NOT NULL OR v.status IN ('paid', 'processing', 'shipped', 'completed', 'refunded')) AS paid
	FROM vendor_orders v JOIN orders o ON o.id = v.order_id
	WHERE o.checkout_state = 'ready' AND v.currency = 'VND'
		AND v.created_at >= $1 AND v.created_at < $2 AND v.created_at <= $4
		AND ($3::text = '' OR v.vendor_id = NULLIF($3::text, '')::uuid)
), returned AS (
	SELECT DISTINCT oi.vendor_order_id AS id FROM return_requests rr JOIN order_items oi ON oi.id = rr.order_item_id
	WHERE rr.status <> 'rejected' AND oi.vendor_order_id IN (SELECT id FROM vo)
)
`

var orderDrillMetrics = []string{"placed_orders", "vendor_orders", "paid_vendor_orders", "cancelled", "paid_cancelled", "returned", "awaiting_fulfillment"}

func (BusinessReports) DrillMetrics() []string { return orderDrillMetrics }

func (r BusinessReports) Report(ctx context.Context, period bizreport.Range) (any, error) {
	out := &domain.BusinessReport{Source: "order", DefinitionVersion: bizreport.DefinitionVersion, Range: period, Currency: "VND",
		Series: []domain.OrderReportBucket{}}
	err := bizreport.Snapshot(ctx, r.Pool, func(tx pgx.Tx, asOf time.Time) error {
		out.AsOf = asOf
		args := []any{period.From, period.To, period.VendorID, asOf}
		var cancelled, paidCancelled, returned int64
		if err := tx.QueryRow(ctx, orderCohort+`SELECT count(DISTINCT order_id), count(*), COALESCE(sum(subtotal_amount), 0)::bigint,
			count(*) FILTER (WHERE paid), count(*) FILTER (WHERE status IN ('paid', 'processing')),
			count(*) FILTER (WHERE status = 'cancelled'), count(*) FILTER (WHERE status = 'cancelled' AND paid),
			count(*) FILTER (WHERE paid AND id IN (SELECT id FROM returned))
			FROM vo`, args...).Scan(&out.PlacedOrders, &out.VendorOrders, &out.GMVOrdered, &out.PaidVendorOrders,
			&out.AwaitingFulfillment, &cancelled, &paidCancelled, &returned); err != nil {
			return err
		}
		out.Cancellation = bizreport.NewRatio(cancelled, out.VendorOrders)
		out.PaidCancellation = bizreport.NewRatio(paidCancelled, out.PaidVendorOrders)
		out.Returns = bizreport.NewRatio(returned, out.PaidVendorOrders)

		rows, err := tx.Query(ctx, orderCohort+`SELECT date_trunc($5, created_at AT TIME ZONE $6) AT TIME ZONE $6 AS bucket,
			count(DISTINCT order_id), count(*), COALESCE(sum(subtotal_amount), 0)::bigint
			FROM vo GROUP BY bucket ORDER BY bucket`, append(args, period.Group, bizreport.ReportZoneName)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b domain.OrderReportBucket
			if err := rows.Scan(&b.Start, &b.PlacedOrders, &b.VendorOrders, &b.GMVOrdered); err != nil {
				rows.Close()
				return err
			}
			b.Start = b.Start.UTC()
			out.Series = append(out.Series, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if period.VendorID != "" {
			return nil
		}
		return r.shops(ctx, tx, args, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// shops ranks the 20 busiest shops of the cohort.
func (r BusinessReports) shops(ctx context.Context, tx pgx.Tx, args []any, out *domain.BusinessReport) error {
	rows, err := tx.Query(ctx, orderCohort+`SELECT vendor_id::text, count(*), COALESCE(sum(subtotal_amount), 0)::bigint,
		count(*) FILTER (WHERE status = 'cancelled'), count(*) FILTER (WHERE paid),
		count(*) FILTER (WHERE paid AND id IN (SELECT id FROM returned))
		FROM vo GROUP BY vendor_id ORDER BY count(*) DESC, vendor_id LIMIT 20`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var line domain.ShopOrderReportLine
		var cancelled, paid, returned int64
		if err := rows.Scan(&line.VendorID, &line.VendorOrders, &line.GMVOrdered, &cancelled, &paid, &returned); err != nil {
			return err
		}
		line.Cancellation = bizreport.NewRatio(cancelled, line.VendorOrders)
		line.Returns = bizreport.NewRatio(returned, paid)
		if line.VendorOrders < bizreport.MinSample {
			line.InsufficientSample = true
			line.Cancellation.Rate, line.Returns.Rate = nil, nil
		}
		out.Shops = append(out.Shops, line)
	}
	return rows.Err()
}

func (r BusinessReports) Drill(ctx context.Context, d bizreport.Drilldown) (any, error) {
	at, id := d.CursorArgs()
	args := []any{d.Range.From, d.Range.To, d.Range.VendorID, d.Cutoff, at, id, d.Limit + 1}
	var query string
	if d.Metric == "placed_orders" {
		// The amount is the scope's own sub-orders, never another shop's.
		query = orderCohort + `SELECT o.id::text, 'order', o.id::text, '', o.status, s.subtotal, 'VND', s.paid, o.created_at
			FROM (SELECT order_id, sum(subtotal_amount)::bigint AS subtotal, bool_or(paid) AS paid FROM vo GROUP BY order_id) s
			JOIN orders o ON o.id = s.order_id
			WHERE (o.created_at, o.id) < ($5, $6::uuid) ORDER BY o.created_at DESC, o.id DESC LIMIT $7`
	} else {
		filter := map[string]string{
			"vendor_orders":        "TRUE",
			"paid_vendor_orders":   "paid",
			"cancelled":            "status = 'cancelled'",
			"paid_cancelled":       "status = 'cancelled' AND paid",
			"returned":             "paid AND id IN (SELECT id FROM returned)",
			"awaiting_fulfillment": "status IN ('paid', 'processing')",
		}[d.Metric]
		if filter == "" {
			return nil, errors.New("unknown drilldown metric")
		}
		query = orderCohort + `SELECT id::text, 'vendor_order', order_id::text, vendor_id::text, status, subtotal_amount, currency, paid, created_at
			FROM vo WHERE ` + filter + ` AND (created_at, id) < ($5, $6::uuid) ORDER BY created_at DESC, id DESC LIMIT $7`
	}
	rows, err := r.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OrderReportRow
	for rows.Next() {
		var row domain.OrderReportRow
		if err := rows.Scan(&row.ID, &row.Kind, &row.OrderID, &row.VendorID, &row.Status, &row.SubtotalAmount, &row.Currency,
			&row.Paid, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bizreport.NewPage(d, out, func(row domain.OrderReportRow) bizreport.Cursor {
		return bizreport.Cursor{At: row.CreatedAt, ID: row.ID}
	}), nil
}
