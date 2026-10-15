package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

func reportOrder(t *testing.T, pool *pgxpool.Pool, at time.Time, ready, paid bool, shops []string, amounts []int64, statuses []string) string {
	t.Helper()
	id := uuid.NewString()
	state := "preparing"
	if ready {
		state = "ready"
	}
	var paidAt *time.Time
	if paid {
		paidAt = &at
	}
	var total int64
	for _, amount := range amounts {
		total += amount
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO orders(id,buyer_id,total_amount,subtotal_amount,currency,recipient_name,phone,province,district,ward,street_address,created_at,checkout_state,paid_at)
		VALUES($1,$2,$3,$3,'VND','Fake buyer','0000000000','Test','Test','Test','Test',$4,$5,$6)`, id, uuid.NewString(), total, at, state, paidAt); err != nil {
		t.Fatal(err)
	}
	for i, shop := range shops {
		if _, err := pool.Exec(t.Context(), `INSERT INTO vendor_orders(id,order_id,vendor_id,subtotal_amount,currency,status,created_at) VALUES($1,$2,$3,$4,'VND',$5,$6)`, uuid.NewString(), id, shop, amounts[i], statuses[i], at); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestBusinessReportOrderCohortAndDrilldown(t *testing.T) {
	pool := orderDB(t)
	from := time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)
	period := bizreport.Range{From: from, To: from.Add(48 * time.Hour), Group: "day"}
	a, b := uuid.NewString(), uuid.NewString()
	// One accepted multi-shop parent; a cancelled paid package remains in GMV.
	reportOrder(t, pool, from, true, true, []string{a, b}, []int64{1000, 2000}, []string{"cancelled", "paid"})
	reportOrder(t, pool, from.Add(24*time.Hour), true, false, []string{a}, []int64{3000}, []string{"pending_payment"})
	reportOrder(t, pool, from.Add(-time.Microsecond), true, true, []string{a}, []int64{99000}, []string{"paid"})
	reportOrder(t, pool, period.To, true, true, []string{a}, []int64{99000}, []string{"paid"})
	reportOrder(t, pool, from.Add(time.Hour), false, false, []string{a}, []int64{99000}, []string{"pending_payment"})
	r := repository.BusinessReports{Pool: pool}
	raw, err := r.Report(t.Context(), period)
	if err != nil {
		t.Fatal(err)
	}
	out := raw.(*domain.BusinessReport)
	if out.PlacedOrders != 2 || out.VendorOrders != 3 || out.GMVOrdered != 6000 || out.PaidVendorOrders != 2 || out.AwaitingFulfillment != 1 {
		t.Fatalf("cohort: %+v", out)
	}
	if out.Cancellation.Numerator != 1 || out.Cancellation.Denominator != 3 || out.PaidCancellation.Numerator != 1 || out.PaidCancellation.Denominator != 2 {
		t.Fatalf("rates: %+v", out)
	}
	if len(out.Series) != 2 || !out.Series[0].Start.Equal(from) || out.Series[0].GMVOrdered != 3000 || out.Series[1].GMVOrdered != 3000 {
		t.Fatalf("Vietnam midnight: %+v", out.Series)
	}
	for _, shop := range out.Shops {
		if !shop.InsufficientSample || shop.Cancellation.Rate != nil || shop.Returns.Rate != nil {
			t.Fatalf("small sample exposed rate: %+v", shop)
		}
	}
	// A new row in the selected period, created after the report, must not leak into drilldown.
	reportOrder(t, pool, out.AsOf.Add(time.Second), true, true, []string{a}, []int64{99000}, []string{"paid"})
	for metric, want := range map[string]int{"placed_orders": 2, "vendor_orders": 3, "paid_vendor_orders": 2, "cancelled": 1, "paid_cancelled": 1, "returned": 0, "awaiting_fulfillment": 1} {
		d := bizreport.Drilldown{Range: period, Metric: metric, Cutoff: out.AsOf, Limit: 1}
		seen := map[string]bool{}
		var sum int64
		for {
			raw, err := r.Drill(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			page := raw.(bizreport.Page[domain.OrderReportRow])
			for _, row := range page.Items {
				if seen[row.ID] {
					t.Fatal("duplicate cursor row")
				}
				seen[row.ID] = true
				sum += row.SubtotalAmount
			}
			if page.NextCursor == "" {
				break
			}
			d, err = bizreport.ParseDrilldown(period, metric, out.AsOf.Format(time.RFC3339Nano), page.NextCursor, "1", r.DrillMetrics())
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != want {
			t.Fatalf("%s count=%d want=%d", metric, len(seen), want)
		}
		if (metric == "placed_orders" || metric == "vendor_orders") && sum != out.GMVOrdered {
			t.Fatalf("%s sum=%d report=%d", metric, sum, out.GMVOrdered)
		}
	}
	period.VendorID = a
	raw, err = r.Report(t.Context(), period)
	if err != nil {
		t.Fatal(err)
	}
	shop := raw.(*domain.BusinessReport)
	if shop.GMVOrdered != 4000 || shop.VendorOrders != 2 || len(shop.Shops) != 0 {
		t.Fatalf("shop scope: %+v", shop)
	}
	raw, err = r.Drill(t.Context(), bizreport.Drilldown{Range: period, Metric: "vendor_orders", Cutoff: shop.AsOf, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range raw.(bizreport.Page[domain.OrderReportRow]).Items {
		if row.VendorID != a {
			t.Fatal("cross-shop row")
		}
	}
	period.VendorID = uuid.NewString()
	raw, err = r.Report(t.Context(), period)
	if err != nil {
		t.Fatal(err)
	}
	empty := raw.(*domain.BusinessReport)
	if empty.PlacedOrders != 0 || empty.Cancellation.Rate != nil || empty.Returns.Rate != nil || empty.PaidCancellation.Rate != nil {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestBusinessReportShopSampleThreshold(t *testing.T) {
	pool := orderDB(t)
	at := time.Now().UTC().Add(-time.Hour)
	shop := uuid.NewString()
	for range 20 {
		reportOrder(t, pool, at, true, false, []string{shop}, []int64{100}, []string{"cancelled"})
	}
	raw, err := (repository.BusinessReports{Pool: pool}).Report(t.Context(), bizreport.Range{From: at.Add(-time.Hour), To: at.Add(time.Hour), Group: "month"})
	if err != nil {
		t.Fatal(err)
	}
	out := raw.(*domain.BusinessReport)
	if len(out.Shops) != 1 || out.Shops[0].InsufficientSample || out.Shops[0].Cancellation.Rate == nil || *out.Shops[0].Cancellation.Rate != 1 {
		t.Fatalf("sample 20: %+v", out.Shops)
	}
}
