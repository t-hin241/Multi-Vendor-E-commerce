// Package adapter holds Catalog's outbound integrations with other
// services. The domain and use case layers depend on the VendorGateway
// interface, never on this HTTP client directly.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// VendorGateway lets the use case check whether a user owns a specific,
// approved vendor (shop) without Catalog owning any vendor data itself. A
// user may own several shops (1:N), so the caller always names which one
// it's acting as; this only confirms that name is legitimate.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error)
}

// VendorNameGateway lets the storefront listing resolve shop names for a
// batch of vendor ids — a separate concern from VendorGateway's single
// ownership check, but served by the same underlying Vendor service.
type VendorNameGateway interface {
	GetShopNames(ctx context.Context, vendorIDs []string) (map[string]string, error)
}

type HTTPVendorClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type vendorStatusResponse struct {
	Data struct {
		VendorID string `json:"vendor_id"`
		Status   string `json:"status"`
	} `json:"data"`
}

// GetApprovedVendorID confirms vendorID both belongs to userID and is
// approved, echoing it back on success. It returns Forbidden if the shop
// isn't userID's or isn't approved yet, so callers can surface a clear
// message without knowing about Vendor's internal status model.
func (c *HTTPVendorClient) GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error) {
	endpoint := fmt.Sprintf("%s/internal/vendors/%s/owned-by/%s", c.baseURL, url.PathEscape(vendorID), url.PathEscape(userID))

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
		return "", apperror.Forbidden("You must have an approved vendor account to sell products")
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("vendor service returned status %d", resp.StatusCode))
	}

	var body vendorStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}

	if body.Data.Status != "approved" {
		return "", apperror.Forbidden("You must have an approved vendor account to sell products")
	}

	return body.Data.VendorID, nil
}

type vendorNamesResponse struct {
	Data []struct {
		VendorID string `json:"vendor_id"`
		ShopName string `json:"shop_name"`
	} `json:"data"`
}

// GetShopNames resolves shop names for a batch of vendor ids in one call.
// Any id Vendor doesn't recognize is simply absent from the result — the
// caller (ProductUseCase.resolveVendorNames) falls back to its own cache
// for whatever's missing.
func (c *HTTPVendorClient) GetShopNames(ctx context.Context, vendorIDs []string) (map[string]string, error) {
	if len(vendorIDs) == 0 {
		return map[string]string{}, nil
	}

	endpoint := fmt.Sprintf("%s/internal/vendors?ids=%s", c.baseURL, url.QueryEscape(strings.Join(vendorIDs, ",")))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vendor service returned status %d", resp.StatusCode)
	}

	var body vendorNamesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(body.Data))
	for _, v := range body.Data {
		out[v.VendorID] = v.ShopName
	}
	return out, nil
}
