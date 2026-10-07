package adapter

import (
	"context"
	"net/http"
	"time"

	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/telemetry"
)

// VendorGateway checks with Vendor that a person may act for a shop with
// one permission (AF-17: the owner, or staff granted it).
type VendorGateway interface {
	EnsureOwnedApproved(ctx context.Context, userID, vendorID, permission string) error
}
type HTTPVendorClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL, key string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: baseURL, key: key, client: telemetry.NewHTTPClient(5 * time.Second)}
}

// EnsureOwnedApproved: a refusal is 403 permission_denied; when the shop
// records cannot be checked nothing is changed (503).
func (c *HTTPVendorClient) EnsureOwnedApproved(ctx context.Context, userID, vendorID, permission string) error {
	_, err := (shopaccess.Client{URL: c.baseURL, Key: c.key, HTTP: c.client}).Authorize(ctx, userID, vendorID, permission)
	return err
}
