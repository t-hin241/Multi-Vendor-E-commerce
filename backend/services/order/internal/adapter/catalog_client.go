package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
)

type ProductInfo struct {
	ID          string
	VendorID    string
	Name        string
	PriceAmount int64
	Currency    string
	IsVisible   bool
	HasVariants bool
	// PackageWeightGrams is nil when the product's category doesn't require
	// packaging weight — kept distinguishable from a genuine 0.
	PackageWeightGrams *int64
}

// VariantOptionInfo is one axis=value label pair of a variant, e.g.
// {AttributeName: "Size", OptionValue: "L"}.
type VariantOptionInfo struct {
	AttributeName string
	OptionValue   string
}

// VariantInfo is a variant resolved just enough for the checkout snapshot:
// which product it belongs to (re-verified against the cart line's own
// product id, since cart state could be stale) and its SKU/option labels
// to snapshot onto the order item.
type VariantInfo struct {
	ID        string
	ProductID string
	SKU       string
	Options   []VariantOptionInfo
}

type HTTPCatalogClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPCatalogClient(baseURL string) *HTTPCatalogClient {
	return &HTTPCatalogClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type internalProductResponse struct {
	Data struct {
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

// GetProduct is the checkout pricing snapshot's source of truth: Order
// calls this fresh for every line at checkout time rather than trusting
// whatever price Cart last displayed, so a price change between "viewed
// cart" and "clicked checkout" can never leak into the order.
func (c *HTTPCatalogClient) GetProduct(ctx context.Context, productID string) (*ProductInfo, error) {
	endpoint := fmt.Sprintf("%s/internal/products/%s", c.baseURL, url.PathEscape(productID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

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
