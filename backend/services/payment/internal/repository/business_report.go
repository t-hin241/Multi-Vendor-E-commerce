package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bizreport"
	"shopee/backend/pkg/vendorreport"
	"shopee/backend/services/payment/internal/domain"
)

// BusinessReports answers AF-20 from Payment's own records, in one
// read-only snapshot. v1 reports VND only. Money is never inferred from an
// Order or Shipment label.
type BusinessReports struct{ Pool *pgxpool.Pool }

var paymentDrillMetrics = []string{"captured", "refunded"}

func (BusinessReports) DrillMetrics() []string { return paymentDrillMetrics }

// Captures recorded in [$1,$2) and at or before $3.
const capturesInPeriod = `SELECT id, order_id, amount, captured_at, captured_at_estimated FROM payment_intents
	WHERE captured_at IS NOT NULL AND currency = 'VND' AND captured_at >= $1 AND captured_at < $2 AND captured_at <= $3`

// Refunds confirmed in [$1,$2) and at or before $3.
const refundsInPeriod = `SELECT id, order_id, vendor_order_id, amount, COALESCE(resolved_at, updated_at) AS recorded_at FROM payment_refunds
	WHERE status = 'succeeded' AND currency = 'VND' AND COALESCE(resolved_at, updated_at) >= $1
		AND COALESCE(resolved_at, updated_at) < $2 AND COALESCE(resolved_at, updated_at) <= $3`

// A shop's ledger entries recorded in [$1,$2), at or before $3, shop $4.
const ledgerInPeriod = `SELECT id, vendor_order_id, entry_type, amount, created_at FROM settlement_entries
	WHERE vendor_id = $4 AND currency = 'VND' AND created_at >= $1 AND created_at < $2 AND created_at <= $3`

func (r BusinessReports) Report(ctx context.Context, period bizreport.Range) (any, error) {
	out := &domain.BusinessReport{Source: "payment", DefinitionVersion: bizreport.DefinitionVersion, Range: period, Currency: "VND",
		Series: []domain.PaymentReportBucket{}}
	err := bizreport.Snapshot(ctx, r.Pool, func(tx pgx.Tx, asOf time.Time) error {
		out.AsOf = asOf
		if period.VendorID != "" {
			return r.shop(ctx, tx, period, out)
		}
		return r.marketplace(ctx, tx, period, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r BusinessReports) marketplace(ctx context.Context, tx pgx.Tx, period bizreport.Range, out *domain.BusinessReport) error {
	out.Scope = "marketplace"
	args := []any{period.From, period.To, out.AsOf}
	var captured, capturedCount, refunded int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount), 0)::bigint, count(*), count(*) FILTER (WHERE captured_at_estimated)
		FROM (`+capturesInPeriod+`) c`, args...).Scan(&captured, &capturedCount, &out.EstimatedCaptures); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount), 0)::bigint, count(*) FROM (`+refundsInPeriod+`) r`, args...).
		Scan(&refunded, &out.RefundCount); err != nil {
		return err
	}
	out.Captured, out.Refunded, out.NetCash = vendorreport.Known(captured), vendorreport.Known(refunded), vendorreport.Known(captured-refunded)
	out.CapturedCount = &capturedCount
	out.SettledSales = vendorreport.Unknown("shop_scope_only")
	out.PaidOut = vendorreport.Unknown("shop_scope_only")
	rows, err := tx.Query(ctx, `WITH m AS (
		SELECT captured_at AS at, amount AS captured, 0::bigint AS refunded FROM (`+capturesInPeriod+`) c
		UNION ALL SELECT recorded_at, 0, amount FROM (`+refundsInPeriod+`) r)
		SELECT date_trunc($4, at AT TIME ZONE $5) AT TIME ZONE $5 AS bucket, sum(captured)::bigint, sum(refunded)::bigint
		FROM m GROUP BY bucket ORDER BY bucket`, append(args, period.Group, bizreport.ReportZoneName)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b domain.PaymentReportBucket
		var c int64
		if err := rows.Scan(&b.Start, &c, &b.Refunded); err != nil {
			return err
		}
		b.Start, b.Captured = b.Start.UTC(), &c
		out.Series = append(out.Series, b)
	}
	return rows.Err()
}

// shop reports from the shop's settlement ledger: refund entries are
// negative, payouts negative, sales and shipping positive.
func (r BusinessReports) shop(ctx context.Context, tx pgx.Tx, period bizreport.Range, out *domain.BusinessReport) error {
	out.Scope = "shop"
	args := []any{period.From, period.To, out.AsOf, period.VendorID}
	var refunded, sales, paidOut int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(-sum(amount) FILTER (WHERE entry_type = 'refund'), 0)::bigint,
		count(*) FILTER (WHERE entry_type = 'refund'),
		COALESCE(sum(amount) FILTER (WHERE entry_type IN ('sale', 'shipping')), 0)::bigint,
		COALESCE(-sum(amount) FILTER (WHERE entry_type = 'payout'), 0)::bigint
		FROM (`+ledgerInPeriod+`) e`, args...).Scan(&refunded, &out.RefundCount, &sales, &paidOut); err != nil {
		return err
	}
	out.Captured = vendorreport.Unknown("vendor_allocation_not_available")
	out.NetCash = vendorreport.Unknown("vendor_allocation_not_available")
	out.Refunded, out.SettledSales, out.PaidOut = vendorreport.Known(refunded), vendorreport.Known(sales), vendorreport.Known(paidOut)
	var legacy int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM payout_items WHERE vendor_id = $1 AND status = 'succeeded' AND currency IS NULL`,
		period.VendorID).Scan(&legacy); err != nil {
		return err
	}
	if legacy > 0 {
		out.PaidOut = vendorreport.Unknown("legacy_payout_currency_requires_review")
	}
	rows, err := tx.Query(ctx, `SELECT date_trunc($5, created_at AT TIME ZONE $6) AT TIME ZONE $6 AS bucket,
		COALESCE(-sum(amount) FILTER (WHERE entry_type = 'refund'), 0)::bigint
		FROM (`+ledgerInPeriod+`) e GROUP BY bucket ORDER BY bucket`, append(args, period.Group, bizreport.ReportZoneName)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var b domain.PaymentReportBucket
		if err := rows.Scan(&b.Start, &b.Refunded); err != nil {
			return err
		}
		b.Start = b.Start.UTC()
		out.Series = append(out.Series, b)
	}
	return rows.Err()
}

func (r BusinessReports) Drill(ctx context.Context, d bizreport.Drilldown) (any, error) {
	at, id := d.CursorArgs()
	var query string
	args := []any{d.Range.From, d.Range.To, d.Cutoff}
	switch {
	case d.Range.VendorID != "" && d.Metric == "captured":
		return nil, apperror.Validation("A shop's captured share is not available; see the shop's statement")
	case d.Range.VendorID != "":
		args = append(args, d.Range.VendorID, at, id, d.Limit+1)
		query = `SELECT id::text, 'ledger_refund', '', COALESCE(vendor_order_id::text, ''), -amount, created_at, false
			FROM (` + ledgerInPeriod + `) e WHERE entry_type = 'refund' AND (created_at, id) < ($5, $6::uuid)
			ORDER BY created_at DESC, id DESC LIMIT $7`
	case d.Metric == "captured":
		args = append(args, at, id, d.Limit+1)
		query = `SELECT id::text, 'payment', order_id::text, '', amount, captured_at, captured_at_estimated
			FROM (` + capturesInPeriod + `) c WHERE (captured_at, id) < ($4, $5::uuid) ORDER BY captured_at DESC, id DESC LIMIT $6`
	default:
		args = append(args, at, id, d.Limit+1)
		query = `SELECT id::text, 'refund', order_id::text, COALESCE(vendor_order_id::text, ''), amount, recorded_at, false
			FROM (` + refundsInPeriod + `) r WHERE (recorded_at, id) < ($4, $5::uuid) ORDER BY recorded_at DESC, id DESC LIMIT $6`
	}
	rows, err := r.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PaymentReportRow
	for rows.Next() {
		row := domain.PaymentReportRow{Currency: "VND"}
		if err := rows.Scan(&row.ID, &row.Kind, &row.OrderID, &row.VendorOrderID, &row.Amount, &row.RecordedAt, &row.Estimated); err != nil {
			return nil, err
		}
		row.RecordedAt = row.RecordedAt.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bizreport.NewPage(d, out, func(row domain.PaymentReportRow) bizreport.Cursor {
		return bizreport.Cursor{At: row.RecordedAt, ID: row.ID}
	}), nil
}
