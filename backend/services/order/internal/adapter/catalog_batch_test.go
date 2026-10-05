package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"shopee/backend/pkg/serviceauth"
)

func TestCatalogBatchDeduplicatesAndMapsByID(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/internal/products/checkout-snapshot" || r.Header.Get(serviceauth.Header) != testKey {
			t.Error("wrong batch/auth contract")
		}
		var in struct {
			ProductIDs []string `json:"product_ids"`
			VariantIDs []string `json:"variant_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(in.ProductIDs, []string{"p1", "p2", "missing"}) || !reflect.DeepEqual(in.VariantIDs, []string{"v1"}) {
			t.Errorf("IDs were not deduplicated: %+v", in)
		}
		fmt.Fprint(w, `{"data":{"products":[{"id":"p2","vendor_id":"shop2","version":7,"price_amount":20,"currency":"VND","is_visible":false},{"id":"p1","vendor_id":"shop1","version":5,"price_amount":10,"currency":"VND","is_visible":true,"has_variants":true,"package_weight_grams":500}],"variants":[{"id":"v1","product_id":"p1","sku":"SKU-L","options":[{"attribute_name":"Size","option_value":"L"}]}]}}`)
	}))
	defer s.Close()
	got, err := NewHTTPCatalogClient(s.URL, testKey).GetCheckoutSnapshot(t.Context(), []string{"p1", "p2", "p1", "missing"}, []string{"v1", "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(got.Products) != 2 || got.Products["missing"] != nil || got.Products["p1"].Version != 5 || *got.Products["p1"].PackageWeightGrams != 500 || got.Products["p2"].IsVisible || got.Variants["v1"].Options[0].OptionValue != "L" {
		t.Fatalf("incorrect snapshot: %+v", got)
	}
}

func TestCatalogBatchFailsClosedWithoutFallback(t *testing.T) {
	product := `{"id":"p","vendor_id":"s","version":1,"price_amount":10,"currency":"VND"}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"old_catalog", 404, `{}`}, {"outage", 503, `{}`}, {"missing_envelope", 200, `{}`},
		{"truncated", 200, `{"data":`}, {"missing_variants", 200, `{"data":{"products":[]}}`},
		{"duplicate", 200, `{"data":{"products":[` + product + `,` + product + `],"variants":[]}}`},
		{"unexpected_id", 200, `{"data":{"products":[],"variants":[{"id":"wrong","product_id":"p","sku":"S"}]}}`},
		{"missing_version", 200, `{"data":{"products":[{"id":"p","vendor_id":"s","price_amount":10,"currency":"VND"}],"variants":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			if _, err := NewHTTPCatalogClient(s.URL, testKey).GetCheckoutSnapshot(t.Context(), []string{"p"}, []string{"v"}); err == nil || calls != 1 {
				t.Fatalf("must fail closed once: calls=%d err=%v", calls, err)
			}
		})
	}
}
