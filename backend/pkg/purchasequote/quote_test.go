package purchasequote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRejectsIncompleteOrMismatchedQuotes(t *testing.T) {
	v := "sku-a"
	input := Input{ProductID: "product-a", VariantID: &v, Quantity: 2}
	for _, tc := range []struct {
		name   string
		mutate func(*Quote)
		valid  bool
	}{
		{"valid", func(q *Quote) {}, true},
		{"missing_version", func(q *Quote) { q.QuoteVersion = "" }, false},
		{"missing_line", func(q *Quote) { q.Lines = nil }, false},
		{"wrong_product", func(q *Quote) { q.Lines[0].ProductID = "other" }, false},
		{"wrong_quantity", func(q *Quote) { q.Lines[0].Quantity = 1 }, false},
		{"product_fallback", func(q *Quote) { q.Lines[0].PricingSource = "product" }, false},
		{"missing_sku_version", func(q *Quote) { q.Lines[0].VariantVersion = 0 }, false},
		{"nonpositive_price", func(q *Quote) { q.Lines[0].UnitPrice = 0 }, false},
		{"wrong_currency", func(q *Quote) { q.Lines[0].Currency = "USD" }, false},
		{"unavailable", func(q *Quote) { q.Lines[0].IsSellable = false; q.Lines[0].UnitPrice = 0 }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quote := Quote{QuoteVersion: "test-version", VariantPricingEnabled: true, Lines: []Line{{Input: input, VendorID: "shop", UnitPrice: 150, Currency: "VND", ProductVersion: 2, VariantVersion: 1, PricingSource: "variant", IsSellable: true}}}
			tc.mutate(&quote)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/internal/catalog/purchase-quotes" {
					t.Error("incorrect quote request")
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"data": quote}); err != nil {
					t.Errorf("encode quote response: %v", err)
				}
			}))
			defer server.Close()
			got, err := (Client{URL: server.URL, Key: "synthetic-test-key"}).PurchaseQuotes(t.Context(), []Input{input})
			if (err == nil) != tc.valid || (err != nil && got != nil) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}
