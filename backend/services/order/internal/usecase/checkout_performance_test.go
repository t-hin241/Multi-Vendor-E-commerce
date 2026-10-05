package usecase_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/usecase"
)

// Opt-in controlled HTTP experiment, not a production load test. Run this
// unchanged before/after an optimization. All persistence is in-memory;
// Catalog and Shipment use the production HTTP adapters and 3 ms/server call.
func TestCheckoutRPCPerformance(t *testing.T) {
	if os.Getenv("CHECKOUT_PERF") != "1" {
		t.Skip("set CHECKOUT_PERF=1 to measure checkout RPC and p95")
	}
	for _, scenario := range []struct{ lines, vendors int }{{1, 1}, {20, 5}, {50, 10}} {
		t.Run(fmt.Sprintf("lines_%d_vendors_%d", scenario.lines, scenario.vendors), func(t *testing.T) {
			var catalogCalls, shipmentCalls atomic.Int64
			products, variants := map[string]any{}, map[string]any{}
			lines := make([]adapter.CartLine, scenario.lines)
			for i := range lines {
				pid, vid := fmt.Sprintf("p-%d", i), fmt.Sprintf("variant-%d", i)
				vendor := fmt.Sprintf("vendor-%02d", i%scenario.vendors)
				products[pid] = map[string]any{"id": pid, "vendor_id": vendor, "version": 1, "name": pid, "price_amount": 100000, "currency": "VND", "is_visible": true, "has_variants": true, "package_weight_grams": 500}
				variants[vid] = map[string]any{"id": vid, "product_id": pid, "sku": vid, "options": []any{map[string]string{"attribute_name": "Size", "option_value": "L"}}}
				lines[i] = adapter.CartLine{ProductID: pid, VariantID: &vid, Quantity: 2}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(3 * time.Millisecond)
				var data any
				switch {
				case r.URL.Path == "/internal/products/checkout-snapshot":
					catalogCalls.Add(1)
					ps, vs := []any{}, []any{}
					for _, p := range products {
						ps = append(ps, p)
					}
					for _, v := range variants {
						vs = append(vs, v)
					}
					data = map[string]any{"products": ps, "variants": vs}
				case strings.HasPrefix(r.URL.Path, "/internal/products/variants/"):
					catalogCalls.Add(1)
					data = variants[strings.TrimPrefix(r.URL.Path, "/internal/products/variants/")]
				case strings.HasPrefix(r.URL.Path, "/internal/products/"):
					catalogCalls.Add(1)
					data = products[strings.TrimPrefix(r.URL.Path, "/internal/products/")]
				case r.URL.Path == "/internal/shipments/quotes":
					shipmentCalls.Add(1)
					var in struct {
						VendorID string `json:"vendor_id"`
						Weight   int64  `json:"package_weight_grams"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					data = map[string]any{"vendor_id": in.VendorID, "fee_amount": 20000, "currency": "VND", "carrier_id": "carrier", "zone_id": "zone", "fee_rule_id": "rule", "fee_rule_version": 1, "package_weight_grams": in.Weight, "quoted_at": time.Now(), "expires_at": time.Now().Add(15 * time.Minute)}
				default:
					t.Errorf("unexpected RPC %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			catalog := adapter.NewHTTPCatalogClient(server.URL, "fake-performance-service-key")
			shipment := adapter.NewHTTPShipmentClient(server.URL, "fake-performance-service-key")
			const samples, warmups = 60, 5
			durations := make([]time.Duration, 0, samples)
			for i := -warmups; i < samples; i++ {
				f := newCheckoutFixture()
				f.cartOf(lines...)
				f.uc.Catalog, f.uc.Shipments = catalog, shipment
				started := time.Now()
				order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: fmt.Sprintf("perf-checkout-%d", i)})
				elapsed := time.Since(started)
				if err != nil {
					t.Fatal(err)
				}
				if order.TotalAmount != int64(scenario.lines*200000+scenario.vendors*20000) || len(f.inventory.reservedOrders) != 1 || len(f.orders.byID) != 1 {
					t.Fatal("checkout correctness changed")
				}
				if i < 0 {
					catalogCalls.Store(0)
					shipmentCalls.Store(0)
				} else {
					durations = append(durations, elapsed)
				}
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			t.Logf("samples=%d HTTP_delay=3ms catalog_RPC/checkout=%.0f shipment_RPC/checkout=%.0f checkout_p50=%s checkout_p95=%s", samples, float64(catalogCalls.Load())/samples, float64(shipmentCalls.Load())/samples, durations[samples/2], durations[(samples*95+99)/100-1])
		})
	}
}
