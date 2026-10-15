package repository_test

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

func reportIntent(t *testing.T, pool *pgxpool.Pool, amount int64, status string, at time.Time) (string, string) {
	t.Helper()
	id, order := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO payment_intents(id,order_id,buyer_id,amount,currency,status,provider,provider_intent_id,created_at,updated_at) VALUES($1,$2,$3,$4,'VND',$5,'test',$7,$6,$6)`, id, order, uuid.NewString(), amount, status, at, "fake-"+id); err != nil {
		t.Fatal(err)
	}
	return id, order
}

func TestBusinessReportPaymentPartialRefundAndCutoff(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	from := time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)
	period := bizreport.Range{From: from, To: from.Add(48 * time.Hour), Group: "day"}
	id, order := reportIntent(t, pool, 10000, "pending", from.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured', captured_at=$2 WHERE id=$1`, id, from); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		amount int64
		status string
		at     time.Time
	}{{2000, "succeeded", from.Add(time.Hour)}, {1000, "succeeded", from.Add(24 * time.Hour)}, {4000, "failed", from.Add(time.Hour)}, {5000, "succeeded", period.To}} {
		if _, err := pool.Exec(ctx, `INSERT INTO payment_refunds(payment_intent_id,order_id,amount,currency,reason,status,requested_by,resolved_at,updated_at) VALUES($1,$2,$3,'VND','fake partial refund',$4,$5,$6,$6)`, id, order, row.amount, row.status, uuid.NewString(), row.at); err != nil {
			t.Fatal(err)
		}
	}
	for _, at := range []time.Time{from.Add(-time.Microsecond), period.To} {
		excluded, _ := reportIntent(t, pool, 99000, "pending", at)
		if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured',captured_at=$2 WHERE id=$1`, excluded, at); err != nil {
			t.Fatal(err)
		}
	}
	r := repository.BusinessReports{Pool: pool}
	raw, err := r.Report(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	out := raw.(*domain.BusinessReport)
	if out.Captured.Value == nil || *out.Captured.Value != 10000 || out.Refunded.Value == nil || *out.Refunded.Value != 3000 || out.NetCash.Value == nil || *out.NetCash.Value != 7000 || out.RefundCount != 2 || out.CapturedCount == nil || *out.CapturedCount != 1 {
		t.Fatalf("cash: %+v", out)
	}
	if len(out.Series) != 2 || !out.Series[0].Start.Equal(from) || out.Series[0].Refunded != 2000 || out.Series[1].Refunded != 1000 {
		t.Fatalf("Vietnam buckets: %+v", out.Series)
	}
	for metric, want := range map[string]int64{"captured": 10000, "refunded": 3000} {
		d := bizreport.Drilldown{Range: period, Metric: metric, Cutoff: out.AsOf, Limit: 1}
		var sum int64
		seen := map[string]bool{}
		for {
			raw, err := r.Drill(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			p := raw.(bizreport.Page[domain.PaymentReportRow])
			for _, row := range p.Items {
				if seen[row.ID] {
					t.Fatal("duplicate row")
				}
				seen[row.ID] = true
				sum += row.Amount
			}
			if p.NextCursor == "" {
				break
			}
			d, err = bizreport.ParseDrilldown(period, metric, out.AsOf.Format(time.RFC3339Nano), p.NextCursor, "1", r.DrillMetrics())
			if err != nil {
				t.Fatal(err)
			}
		}
		if sum != want {
			t.Fatalf("%s drill=%d report=%d", metric, sum, want)
		}
	}
	// A later capture in the SAME requested range cannot appear under the old cutoff.
	wide := bizreport.Range{From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC().Add(time.Hour), Group: "day"}
	raw, err = r.Report(ctx, wide)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := raw.(*domain.BusinessReport).AsOf
	later, _ := reportIntent(t, pool, 8888, "pending", cutoff)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured',captured_at=$2 WHERE id=$1`, later, cutoff.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	raw, err = r.Drill(ctx, bizreport.Drilldown{Range: wide, Metric: "captured", Cutoff: cutoff, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.(bizreport.Page[domain.PaymentReportRow]).Items) != 0 {
		t.Fatal("post-cutoff capture leaked")
	}
}

func TestBusinessReportShopLedgerMatchesStatement(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	a, b := uuid.NewString(), uuid.NewString()
	at := time.Now().UTC().Add(-time.Minute)
	for _, shop := range []string{a, b} {
		for kind, amount := range map[string]int64{"sale": 10000, "shipping": 1000, "commission": -1000, "refund": -2000, "payout": -3000} {
			if _, err := pool.Exec(ctx, `INSERT INTO settlement_entries(vendor_id,entry_type,amount,currency,source_ref,eligible_at,created_at) VALUES($1,$2,$3,'VND',$4,$5,$5)`, shop, kind, amount, uuid.NewString(), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	period := bizreport.Range{From: at.Add(-time.Hour), To: at.Add(time.Hour), Group: "month", VendorID: a}
	r := repository.BusinessReports{Pool: pool}
	raw, err := r.Report(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	out := raw.(*domain.BusinessReport)
	if out.Captured.Available || out.Captured.Value != nil || out.NetCash.Available || out.CapturedCount != nil || *out.Refunded.Value != 2000 || *out.PaidOut.Value != 3000 || *out.SettledSales.Value != 11000 {
		t.Fatalf("shop ledger: %+v", out)
	}
	statement, err := (repository.StatementRepository{Pool: pool}).Read(ctx, a, domain.StatementFilter{From: period.From, To: period.To, Currency: "VND"}, nil, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if statement.Balances.Closing != 5000 {
		t.Fatalf("statement closing=%d", statement.Balances.Closing)
	}
	raw, err = r.Drill(ctx, bizreport.Drilldown{Range: period, Metric: "refunded", Cutoff: out.AsOf, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	p := raw.(bizreport.Page[domain.PaymentReportRow])
	if len(p.Items) != 1 || p.Items[0].Amount != *out.Refunded.Value {
		t.Fatalf("shop refund drill: %+v", p)
	}
	if _, err := r.Drill(ctx, bizreport.Drilldown{Range: period, Metric: "captured", Cutoff: out.AsOf, Limit: 100}); err == nil {
		t.Fatal("invented shop capture allocation")
	}
}

func TestCapturedAtTriggerLateCaptureAndMigrationBackfill(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	id, _ := reportIntent(t, pool, 1000, "pending", old)
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='failed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var at *time.Time
	if err := pool.QueryRow(ctx, `SELECT captured_at FROM payment_intents WHERE id=$1`, id).Scan(&at); err != nil || at != nil {
		t.Fatalf("failed counted as capture: %v %v", at, err)
	}
	before := time.Now().UTC()
	if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status='captured' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var estimated bool
	if err := pool.QueryRow(ctx, `SELECT captured_at,captured_at_estimated FROM payment_intents WHERE id=$1`, id).Scan(&at, &estimated); err != nil || at == nil || at.Before(before) || estimated {
		t.Fatalf("late capture: %v estimated=%v %v", at, estimated, err)
	}
	first := *at
	for _, status := range []string{"captured", "refunded", "captured"} {
		if _, err := pool.Exec(ctx, `UPDATE payment_intents SET status=$2 WHERE id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT captured_at FROM payment_intents WHERE id=$1`, id).Scan(&at); err != nil || !at.Equal(first) {
			t.Fatalf("capture time moved: %v %v", at, err)
		}
	}
	// Exercise the actual down/up files in this isolated schema with legacy data.
	down, err := os.ReadFile("../../migrations/000021_business_reports.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	withSync, _ := reportIntent(t, pool, 2000, "captured", old)
	refunded, _ := reportIntent(t, pool, 3000, "refunded", old)
	pending, _ := reportIntent(t, pool, 4000, "pending", old)
	failed, _ := reportIntent(t, pool, 5000, "failed", old)
	syncAt := old.Add(time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO payment_order_sync(payment_intent_id,outcome,created_at) VALUES($1,'captured',$2)`, withSync, syncAt); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/000021_business_reports.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want *time.Time
	}{{withSync, &syncAt}, {refunded, &old}, {pending, nil}, {failed, nil}} {
		if err := pool.QueryRow(ctx, `SELECT captured_at,captured_at_estimated FROM payment_intents WHERE id=$1`, tc.id).Scan(&at, &estimated); err != nil {
			t.Fatal(err)
		}
		if (at == nil) != (tc.want == nil) || at != nil && (!at.Equal(*tc.want) || !estimated) || at == nil && estimated {
			t.Fatalf("backfill %s: %v estimated=%v want=%v", tc.id, at, estimated, tc.want)
		}
	}
}
