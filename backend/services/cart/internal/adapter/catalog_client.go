// Package adapter holds Cart's outbound integration with Catalog. The use
// case depends on the CatalogGateway interface declared in the usecase
// package, never on this HTTP client directly.
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
}

// VariantOptionInfo is one axis=value label pair of a variant, e.g.
// {AttributeName: "Size", OptionValue: "L"}.
type VariantOptionInfo struct {
	AttributeName string
	OptionValue   string
}

// VariantInfo is a variant resolved just enough for Cart to validate it
// belongs to the product it was added against, and to display it on a
// cart line.
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
		ID          string `json:"id"`
		VendorID    string `json:"vendor_id"`
		Name        string `json:"name"`
		PriceAmount int64  `json:"price_amount"`
		Currency    string `json:"currency"`
		IsVisible   bool   `json:"is_visible"`
		HasVariants bool   `json:"has_variants"`
	} `json:"data"`
}

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
		ID:          body.Data.ID,
		VendorID:    body.Data.VendorID,
		Name:        body.Data.Name,
		PriceAmount: body.Data.PriceAmount,
		Currency:    body.Data.Currency,
		IsVisible:   body.Data.IsVisible,
		HasVariants: body.Data.HasVariants,
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

// GetVariant resolves a variant's product/SKU/option labels, so AddItem can
// verify it actually belongs to the product it was submitted for, and so a
// cart line can display something more useful than a bare id.
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
