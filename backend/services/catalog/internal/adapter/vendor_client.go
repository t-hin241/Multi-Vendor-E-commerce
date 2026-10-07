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
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/pkg/vendorsales"
)

// VendorGateway lets the use case check whether a user may act with one
// permission for a specific, approved shop without Catalog owning any
// vendor data itself. The caller names the shop (or derives it from the
// product); this only confirms the person may act for it.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID, permission string) (string, error)
}

// VendorNameGateway lets the storefront listing resolve shop names for a
// batch of vendor ids — a separate concern from VendorGateway's single
// ownership check, but served by the same underlying Vendor service.
type VendorNameGateway interface {
	GetShopNames(ctx context.Context, vendorIDs []string) (map[string]string, error)
}

type HTTPVendorClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL, key string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: baseURL, key: key, client: telemetry.NewHTTPClient(5 * time.Second)}
}

// GetApprovedVendorID confirms userID holds permission on the approved
// shop vendorID (AF-17: the owner, or staff granted it), echoing the id
// back. A refusal is 403 permission_denied, an unknown answer 503.
func (c *HTTPVendorClient) GetApprovedVendorID(ctx context.Context, userID, vendorID, permission string) (string, error) {
	grant, err := (shopaccess.Client{URL: c.baseURL, Key: c.key, HTTP: c.client}).Authorize(ctx, userID, vendorID, permission)
	if err != nil {
		return "", err
	}
	if grant.Status != "approved" {
		return "", apperror.Forbidden("You must have an approved vendor account to sell products")
	}
	return grant.VendorID, nil
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

	serviceauth.SetRequestHeaders(req, c.key)
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

func (c *HTTPVendorClient) Approved(ctx context.Context, ids []string) (map[string]int64, error) {
	return (vendorsales.Client{URL: c.baseURL, Key: c.key}).Approved(ctx, ids)
}
