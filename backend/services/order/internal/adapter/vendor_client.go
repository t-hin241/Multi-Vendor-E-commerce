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

// GetApprovedVendorID lets a vendor user see their own sub-orders without
// Order owning any vendor data itself.
func (c *HTTPVendorClient) GetApprovedVendorID(ctx context.Context, userID string) (string, error) {
	endpoint := fmt.Sprintf("%s/internal/vendors/by-user/%s", c.baseURL, url.PathEscape(userID))

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
		return "", apperror.Forbidden("You must have an approved vendor account to view vendor orders")
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("vendor service returned status %d", resp.StatusCode))
	}

	var body vendorStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}
	if body.Data.Status != "approved" {
		return "", apperror.Forbidden("You must have an approved vendor account to view vendor orders")
	}

	return body.Data.VendorID, nil
}
