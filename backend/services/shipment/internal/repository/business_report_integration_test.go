package repository_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

func TestBusinessReportShipmentCohortLatencyAndDrilldown(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	from := time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)
	period := bizreport.Range{From: from, To: from.Add(24 * time.Hour), Group: "day", VendorID: e.vendorA}
	for _, tc := range []struct {
		status    string
		hours     int
		delivered bool
		at        time.Time
	}{
		{"delivered", 72, true, from}, // exactly 72h is not late
		{"shipped", 73, false, from.Add(time.Hour)},
		{"pending", 0, false, from.Add(2 * time.Hour)},
		{"cancelled", 0, false, from.Add(3 * time.Hour)},
		{"pending", 0, false, period.To}, // exclusive end
	} {
		s := e.paidShipment(t)
		var shipped, delivered *time.Time
		if tc.hours > 0 {
			v := tc.at.Add(time.Duration(tc.hours) * time.Hour)
			shipped = &v
		}
		if tc.delivered {
			v := shipped.Add(24 * time.Hour)
			delivered = &v
		}
		if _, err := e.pool.Exec(ctx, `UPDATE shipments SET status=$2,created_at=$3,shipped_at=$4,delivered_at=$5 WHERE id=$1`, s.ID, tc.status, tc.at, shipped, delivered); err != nil {
			t.Fatal(err)
		}
	}
	r := repository.BusinessReports{Pool: e.pool}
	raw, err := r.Report(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	out := raw.(*domain.BusinessReport)
	if out.Fulfillable != 4 || out.HandedOff != 2 || out.Delivered != 1 || out.Cancelled != 1 || out.LateHandoff.Numerator != 2 || out.LateHandoff.Denominator != 3 {
		t.Fatalf("cohort: %+v", out)
	}
	if out.HandoffHours == nil || out.HandoffHours.Count != 2 || out.HandoffHours.Average != 72.5 || math.Abs(out.HandoffHours.P90-72.9) > 0.0001 || out.DeliveryHours == nil || out.DeliveryHours.Average != 24 {
		t.Fatalf("latencies: %+v %+v", out.HandoffHours, out.DeliveryHours)
	}
	if len(out.Series) != 1 || !out.Series[0].Start.Equal(from) || out.Series[0].Fulfillable != 4 {
		t.Fatalf("Vietnam day: %+v", out.Series)
	}
	for metric, want := range map[string]int{"fulfillable": 4, "handed_off": 2, "late_handoff": 2, "delivered": 1, "cancelled": 1, "returned": 0, "lost": 0} {
		d := bizreport.Drilldown{Range: period, Metric: metric, Cutoff: out.AsOf, Limit: 1}
		seen := map[string]bool{}
		for {
			raw, err := r.Drill(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			p := raw.(bizreport.Page[domain.ShipmentReportRow])
			for _, row := range p.Items {
				if row.VendorID != e.vendorA || seen[row.ID] {
					t.Fatalf("scope/duplicate: %+v", row)
				}
				seen[row.ID] = true
			}
			if p.NextCursor == "" {
				break
			}
			d, err = bizreport.ParseDrilldown(period, metric, out.AsOf.Format(time.RFC3339Nano), p.NextCursor, "1", r.DrillMetrics())
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != want {
			t.Fatalf("%s drill=%d report=%d", metric, len(seen), want)
		}
	}
	period.VendorID = uuid.NewString()
	raw, err = r.Report(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	empty := raw.(*domain.BusinessReport)
	if empty.Fulfillable != 0 || empty.LateHandoff.Rate != nil || empty.HandoffHours != nil || empty.DeliveryHours != nil {
		t.Fatalf("empty: %+v", empty)
	}
}
