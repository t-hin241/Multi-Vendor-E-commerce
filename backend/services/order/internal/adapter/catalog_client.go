package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

type ProductInfo struct {
	Version     int64  `json:"version"`
	ID          string `json:"id"`
	VendorID    string `json:"vendor_id"`
	Name        string `json:"name"`
	PriceAmount int64  `json:"price_amount"`
	Currency    string `json:"currency"`
	IsVisible   bool   `json:"is_visible"`
	HasVariants bool   `json:"has_variants"`
	// PackageWeightGrams is nil when the product's category doesn't require
	// packaging weight — kept distinguishable from a genuine 0.
	PackageWeightGrams *int64 `json:"package_weight_grams"`
}

// VariantOptionInfo is one axis=value label pair of a variant, e.g.
// {AttributeName: "Size", OptionValue: "L"}.
type VariantOptionInfo struct {
	AttributeName string `json:"attribute_name"`
	OptionValue   string `json:"option_value"`
}

// VariantInfo is a variant resolved just enough for the checkout snapshot:
// which product it belongs to (re-verified against the cart line's own
// product id, since cart state could be stale) and its SKU/option labels
// to snapshot onto the order item.
type VariantInfo struct {
	ID        string              `json:"id"`
	ProductID string              `json:"product_id"`
	SKU       string              `json:"sku"`
	Options   []VariantOptionInfo `json:"options"`
}

type HTTPCatalogClient struct {
	key     string
	baseURL string
	client  *http.Client
}

func NewHTTPCatalogClient(baseURL, key string) *HTTPCatalogClient {
	return &HTTPCatalogClient{key: key, baseURL: baseURL, client: telemetry.NewHTTPClient(5 * time.Second)}
}

type internalProductResponse struct {
	Data struct {
		Version            int64  `json:"version"`
		ID                 string `json:"id"`
		VendorID           string `json:"vendor_id"`
		Name               string `json:"name"`
		PriceAmount        int64  `json:"price_amount"`
		Currency           string `json:"currency"`
		IsVisible          bool   `json:"is_visible"`
		HasVariants        bool   `json:"has_variants"`
		PackageWeightGrams *int64 `json:"package_weight_grams"`
	} `json:"data"`
}

// GetProduct supports legacy effects that need one product's shipping weight.
// Checkout and preview use GetCheckoutSnapshot to read all facts together.
func (c *HTTPCatalogClient) GetProduct(ctx context.Context, productID string) (*ProductInfo, error) {
	endpoint := fmt.Sprintf("%s/internal/products/%s", c.baseURL, url.PathEscape(productID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperror.NotFound("Product not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("catalog service returned status %d", resp.StatusCode))
	}

	var body internalProductResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	return &ProductInfo{
		Version:            body.Data.Version,
		ID:                 body.Data.ID,
		VendorID:           body.Data.VendorID,
		Name:               body.Data.Name,
		PriceAmount:        body.Data.PriceAmount,
		Currency:           body.Data.Currency,
		IsVisible:          body.Data.IsVisible,
		HasVariants:        body.Data.HasVariants,
		PackageWeightGrams: body.Data.PackageWeightGrams,
	}, nil
}

type internalVariantResponse struct {
	Data struct {
		ID        string `json:"id"`
		ProductID string `json:"product_id"`
		SKU       string `json:"sku"`
		Options   []struct {
			AttributeName string `json:"attribute_name"`
			OptionValue   string `json:"option_value"`
		} `json:"options"`
	} `json:"data"`
}

// GetVariant resolves a variant's product/SKU/option labels for the
// checkout snapshot.
func (c *HTTPCatalogClient) GetVariant(ctx context.Context, variantID string) (*VariantInfo, error) {
	endpoint := fmt.Sprintf("%s/internal/products/variants/%s", c.baseURL, url.PathEscape(variantID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperror.NotFound("Product option not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("catalog service returned status %d", resp.StatusCode))
	}

	var body internalVariantResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	options := make([]VariantOptionInfo, 0, len(body.Data.Options))
	for _, o := range body.Data.Options {
		options = append(options, VariantOptionInfo{AttributeName: o.AttributeName, OptionValue: o.OptionValue})
	}

	return &VariantInfo{ID: body.Data.ID, ProductID: body.Data.ProductID, SKU: body.Data.SKU, Options: options}, nil
}

type CatalogSnapshot struct {
	Products map[string]*ProductInfo
	Variants map[string]*VariantInfo
}

// GetCheckoutSnapshot is a fresh read, never a cache. Missing IDs remain
// absent so Order can return the same cart-validation errors as single reads.
// An unsupported endpoint or malformed response fails closed; no N+1 retry.
func (c *HTTPCatalogClient) GetCheckoutSnapshot(ctx context.Context, productIDs, variantIDs []string) (*CatalogSnapshot, error) {
	productIDs, variantIDs = uniqueCatalogIDs(productIDs), uniqueCatalogIDs(variantIDs)
	if len(productIDs) == 0 || len(productIDs) > 50 || len(variantIDs) > 50 {
		return nil, apperror.Validation("Invalid checkout catalog batch size")
	}
	payload, err := json.Marshal(struct {
		ProductIDs []string `json:"product_ids"`
		VariantIDs []string `json:"variant_ids"`
	}{productIDs, variantIDs})
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/products/checkout-snapshot", bytes.NewReader(payload))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("catalog snapshot returned status %d", resp.StatusCode))
	}
	var body struct {
		Data *struct {
			Products []ProductInfo `json:"products"`
			Variants []VariantInfo `json:"variants"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}
	invalid := func() (*CatalogSnapshot, error) {
		return nil, apperror.Internal(fmt.Errorf("invalid catalog snapshot response"))
	}
	if body.Data == nil || body.Data.Products == nil || body.Data.Variants == nil {
		return invalid()
	}
	requestedProducts, requestedVariants := map[string]bool{}, map[string]bool{}
	for _, id := range productIDs {
		requestedProducts[id] = true
	}
	for _, id := range variantIDs {
		requestedVariants[id] = true
	}
	out := &CatalogSnapshot{Products: map[string]*ProductInfo{}, Variants: map[string]*VariantInfo{}}
	for _, p := range body.Data.Products {
		if !requestedProducts[p.ID] || out.Products[p.ID] != nil || p.Version <= 0 || p.VendorID == "" || p.Currency == "" || p.PriceAmount <= 0 {
			return invalid()
		}
		out.Products[p.ID] = &p
	}
	for _, v := range body.Data.Variants {
		if !requestedVariants[v.ID] || out.Variants[v.ID] != nil || v.ProductID == "" || v.SKU == "" {
			return invalid()
		}
		out.Variants[v.ID] = &v
	}
	return out, nil
}

func uniqueCatalogIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
