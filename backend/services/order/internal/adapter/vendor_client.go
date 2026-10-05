package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/pkg/vendorsales"
)

type HTTPVendorClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL, key string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: baseURL, key: key, client: telemetry.NewHTTPClient(5 * time.Second)}
}

type vendorStatusResponse struct {
	Data struct {
		VendorID string `json:"vendor_id"`
		Status   string `json:"status"`
	} `json:"data"`
}

// GetApprovedVendorID allows an approved or suspended owner to fulfill existing orders.
func (c *HTTPVendorClient) GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error) {
	endpoint := fmt.Sprintf("%s/internal/vendors/%s/owned-by/%s", c.baseURL, url.PathEscape(vendorID), url.PathEscape(userID))

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

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return "", apperror.Forbidden("You must have an approved vendor account to view vendor orders")
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("vendor service returned status %d", resp.StatusCode))
	}

	var body vendorStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}
	if body.Data.Status != "approved" && body.Data.Status != "suspended" {
		return "", apperror.Forbidden("You must have an approved vendor account to view vendor orders")
	}

	return body.Data.VendorID, nil
}

func (c *HTTPVendorClient) Approved(ctx context.Context, ids []string) (map[string]int64, error) {
	return (vendorsales.Client{URL: c.baseURL, Key: c.key}).Approved(ctx, ids)
}
