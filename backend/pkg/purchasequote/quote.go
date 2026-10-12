// Package purchasequote defines Catalog's authoritative purchase quote contract.
// Consumers never derive a SKU's price from a product projection.
package purchasequote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

type Input struct {
	ProductID string  `json:"product_id"`
	VariantID *string `json:"variant_id,omitempty"`
	Quantity  int64   `json:"quantity"`
}
type Line struct {
	Input
	VendorID       string `json:"vendor_id"`
	ProductName    string `json:"product_name"`
	SKU            string `json:"sku,omitempty"`
	VariantLabel   string `json:"variant_label,omitempty"`
	UnitPrice      int64  `json:"unit_price"`
	Currency       string `json:"currency"`
	ProductVersion int64  `json:"product_version"`
	VariantVersion int64  `json:"variant_version"`
	PricingSource  string `json:"pricing_source"`
	IsSellable     bool   `json:"is_sellable"`
	ProductFound   bool   `json:"product_found"`
	VariantFound   bool   `json:"variant_found"`
	HasVariants    bool   `json:"has_variants"`
	Status         string `json:"status"`
	PackageWeight  *int64 `json:"package_weight"`
}
type Quote struct {
	Lines                 []Line `json:"lines"`
	QuoteVersion          string `json:"quote_version"`
	VariantPricingEnabled bool   `json:"variant_pricing_enabled"`
}

func Key(product string, variant *string) string {
	if variant != nil {
		return product + ":" + *variant
	}
	return product
}
func PriceChanged() *apperror.Error {
	return &apperror.Error{Code: "price_changed", Status: 409, Message: "A price changed; review and confirm your cart again"}
}
func Unavailable() *apperror.Error {
	return &apperror.Error{Code: "variant_not_sellable", Status: 409, Message: "The selected product option is not currently available"}
}

type Client struct{ URL, Key string }

func (c Client) PurchaseQuotes(ctx context.Context, lines []Input) (*Quote, error) {
	data, err := json.Marshal(struct {
		Lines []Input `json:"lines"`
	}{lines})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/internal/catalog/purchase-quotes", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", middleware.RequestIDFromContext(ctx))
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := telemetry.NewHTTPClient(3 * time.Second).Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, apperror.Internal(fmt.Errorf("catalog purchase quotes returned %d", resp.StatusCode))
	}
	var body struct {
		Data *Quote `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}
	if body.Data == nil || body.Data.QuoteVersion == "" || len(body.Data.Lines) != len(lines) {
		return nil, apperror.Internal(fmt.Errorf("invalid catalog quote envelope"))
	}
	for i, q := range body.Data.Lines {
		in := lines[i]
		if Key(q.ProductID, q.VariantID) != Key(in.ProductID, in.VariantID) || q.Quantity != in.Quantity {
			return nil, apperror.Internal(fmt.Errorf("catalog quote line mismatch"))
		}
		if q.IsSellable && (q.UnitPrice <= 0 || q.Currency != "VND" || q.ProductVersion < 1 || q.VendorID == "" || (q.VariantID != nil && (q.VariantVersion < 1 || q.PricingSource != "variant")) || (q.VariantID == nil && q.PricingSource != "product")) {
			return nil, apperror.Internal(fmt.Errorf("invalid sellable catalog quote"))
		}
	}
	return body.Data, nil
}
