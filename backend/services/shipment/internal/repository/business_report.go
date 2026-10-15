package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/shipment/internal/domain"
)

// BusinessReports answers AF-20 from Shipment's own table, in one
// read-only snapshot.
type BusinessReports struct{ Pool *pgxpool.Pool }

// shipmentCohort: first attempts opened in [$1,$2) at or before $4, shop
// $3 (empty = all); late uses $5 (seconds) against $4 for open packages.
const shipmentCohort = `WITH s AS (
	SELECT id, vendor_order_id, vendor_id, status, created_at, shipped_at, delivered_at, returned_at,
		status <> 'cancelled' AND COALESCE(shipped_at, $4) - created_at > make_interval(secs => $5) AS late
	FROM shipments
	WHERE attempt_no = 1 AND created_at >= $1 AND created_at < $2 AND created_at <= $4
		AND ($3::text = '' OR vendor_id = NULLIF($3::text, '')::uuid)
)
`

var shipmentDrillMetrics = []string{"fulfillable", "handed_off", "late_handoff", "delivered", "returned", "lost", "cancelled"}

var shipmentDrillFilters = map[string]string{
	"fulfillable":  "TRUE",
	"handed_off":   "shipped_at IS NOT NULL",
	"late_handoff": "late",
	"delivered":    "delivered_at IS NOT NULL",
	"returned":     "(status = 'returned' OR returned_at IS NOT NULL)",
	"lost":         "status = 'lost'",
	"cancelled":    "status = 'cancelled'",
}

func (BusinessReports) DrillMetrics() []string { return shipmentDrillMetrics }

func (r BusinessReports) Report(ctx context.Context, period bizreport.Range) (any, error) {
	out := &domain.BusinessReport{Source: "shipment", DefinitionVersion: bizreport.DefinitionVersion, Range: period,
		Series: []domain.ShipmentReportBucket{}}
	err := bizreport.Snapshot(ctx, r.Pool, func(tx pgx.Tx, asOf time.Time) error {
		out.AsOf = asOf
		args := []any{period.From, period.To, period.VendorID, asOf, domain.LateHandoff.Seconds()}
		var late int64
		if err := tx.QueryRow(ctx, shipmentCohort+`SELECT count(*), count(*) FILTER (WHERE shipped_at IS NOT NULL),
			count(*) FILTER (WHERE delivered_at IS NOT NULL), count(*) FILTER (WHERE status = 'returned' OR returned_at IS NOT NULL),
			count(*) FILTER (WHERE status = 'lost'), count(*) FILTER (WHERE status = 'cancelled'), count(*) FILTER (WHERE late)
			FROM s`, args...).Scan(&out.Fulfillable, &out.HandedOff, &out.Delivered, &out.Returned, &out.Lost, &out.Cancelled, &late); err != nil {
			return err
		}
		out.LateHandoff = bizreport.NewRatio(late, out.Fulfillable-out.Cancelled)
		var err error
		if out.HandoffHours, err = hours(ctx, tx, shipmentCohort+`SELECT EXTRACT(EPOCH FROM shipped_at - created_at) / 3600 AS h FROM s WHERE shipped_at IS NOT NULL`, args); err != nil {
			return err
		}
		if out.DeliveryHours, err = hours(ctx, tx, shipmentCohort+`SELECT EXTRACT(EPOCH FROM delivered_at - shipped_at) / 3600 AS h FROM s
			WHERE shipped_at IS NOT NULL AND delivered_at IS NOT NULL`, args); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, shipmentCohort+`SELECT date_trunc($6, created_at AT TIME ZONE $7) AT TIME ZONE $7 AS bucket, count(*),
			count(*) FILTER (WHERE shipped_at IS NOT NULL), count(*) FILTER (WHERE delivered_at IS NOT NULL)
			FROM s GROUP BY bucket ORDER BY bucket`, append(args, period.Group, bizreport.ReportZoneName)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b domain.ShipmentReportBucket
			if err := rows.Scan(&b.Start, &b.Fulfillable, &b.HandedOff, &b.Delivered); err != nil {
				return err
			}
			b.Start = b.Start.UTC()
			out.Series = append(out.Series, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// hours summarizes the "h" column of query; nil when no row.
func hours(ctx context.Context, tx pgx.Tx, query string, args []any) (*domain.HoursSummary, error) {
	var count int64
	var avg, p90 *float64
	if err := tx.QueryRow(ctx, `SELECT count(*), avg(h)::float8, percentile_cont(0.9) WITHIN GROUP (ORDER BY h)::float8 FROM (`+query+`) m`, args...).
		Scan(&count, &avg, &p90); err != nil {
		return nil, err
	}
	if count == 0 || avg == nil || p90 == nil {
		return nil, nil
	}
	return &domain.HoursSummary{Average: *avg, P90: *p90, Count: count}, nil
}

func (r BusinessReports) Drill(ctx context.Context, d bizreport.Drilldown) (any, error) {
	filter, ok := shipmentDrillFilters[d.Metric]
	if !ok {
		return nil, errors.New("unknown drilldown metric")
	}
	at, id := d.CursorArgs()
	rows, err := r.Pool.Query(ctx, shipmentCohort+`SELECT id::text, vendor_order_id::text, vendor_id::text, status, created_at, shipped_at, delivered_at
		FROM s WHERE `+filter+` AND (created_at, id) < ($6, $7::uuid) ORDER BY created_at DESC, id DESC LIMIT $8`,
		d.Range.From, d.Range.To, d.Range.VendorID, d.Cutoff, domain.LateHandoff.Seconds(), at, id, d.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ShipmentReportRow
	for rows.Next() {
		var row domain.ShipmentReportRow
		if err := rows.Scan(&row.ID, &row.VendorOrderID, &row.VendorID, &row.Status, &row.CreatedAt, &row.ShippedAt, &row.DeliveredAt); err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bizreport.NewPage(d, out, func(row domain.ShipmentReportRow) bizreport.Cursor {
		return bizreport.Cursor{At: row.CreatedAt, ID: row.ID}
	}), nil
}
