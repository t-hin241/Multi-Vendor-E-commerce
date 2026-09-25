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

type HTTPCatalogClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPCatalogClient(baseURL string) *HTTPCatalogClient {
	return &HTTPCatalogClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type internalProductResponse struct {
	Data struct {
		ID       string `json:"id"`
		VendorID string `json:"vendor_id"`
		Status   string `json:"status"`
	} `json:"data"`
}

// GetProductOwnerVendorID lets Inventory verify a product belongs to the
// vendor asking to set its stock, without owning any product data itself.
func (c *HTTPCatalogClient) GetProductOwnerVendorID(ctx context.Context, productID string) (string, error) {
	endpoint := fmt.Sprintf("%s/internal/products/%s", c.baseURL, url.PathEscape(productID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", apperror.Internal(err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", apperror.NotFound("Product not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("catalog service returned status %d", resp.StatusCode))
	}

	var body internalProductResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}

	return body.Data.VendorID, nil
}

// GetProductStatus resolves a product's current moderation status, so
// RequestRestock can gate "add stock" on the product already being
// approved, without Inventory owning any product data itself.
func (c *HTTPCatalogClient) GetProductStatus(ctx context.Context, productID string) (string, error) {
	endpoint := fmt.Sprintf("%s/internal/products/%s", c.baseURL, url.PathEscape(productID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", apperror.Internal(err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", apperror.NotFound("Product not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("catalog service returned status %d", resp.StatusCode))
	}

	var body internalProductResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}

	return body.Data.Status, nil
}

type internalVariantOwnerResponse struct {
	Data struct {
		ProductID string `json:"product_id"`
		VendorID  string `json:"vendor_id"`
	} `json:"data"`
}

// GetVariantOwner lets Inventory verify a variant belongs to the vendor
// asking to manage its stock, resolving the variant's product id along the
// way — a client-supplied product_id is never trusted for a variant-scoped
// request, only what this lookup resolves.
func (c *HTTPCatalogClient) GetVariantOwner(ctx context.Context, variantID string) (vendorID, productID string, err error) {
	endpoint := fmt.Sprintf("%s/internal/products/variants/%s", c.baseURL, url.PathEscape(variantID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", apperror.Internal(err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", "", apperror.NotFound("Variant not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", apperror.Internal(fmt.Errorf("catalog service returned status %d", resp.StatusCode))
	}

	var body internalVariantOwnerResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", apperror.Internal(err)
	}

	return body.Data.VendorID, body.Data.ProductID, nil
}
