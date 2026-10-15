package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/inventory/internal/domain"
)

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

	serviceauth.SetRequestHeaders(req, c.key)
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

	serviceauth.SetRequestHeaders(req, c.key)
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

	serviceauth.SetRequestHeaders(req, c.key)
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

func (c *HTTPCatalogClient) InvalidateStock(ctx context.Context, ids []string) error {
	data, err := json.Marshal(map[string]any{"variant_ids": ids})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/internal/stock-cache/invalidate", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("catalog cache invalidation unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("catalog cache invalidation returned %d", resp.StatusCode)
	}
	return nil
}

// VendorTargets resolves SKUs and products of one shop with Catalog (AF-16
// stock imports). Catalog returns only the shop's own rows.
func (c *HTTPCatalogClient) VendorTargets(ctx context.Context, vendor string, skus, variantIDs, productIDs []string) ([]domain.CatalogTarget, error) {
	data, err := json.Marshal(map[string]any{"vendor_id": vendor, "skus": skus, "variant_ids": variantIDs, "product_ids": productIDs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/catalog/vendor-targets", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog target lookup unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog target lookup returned %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Targets []domain.CatalogTarget `json:"targets"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data.Targets, nil
}
