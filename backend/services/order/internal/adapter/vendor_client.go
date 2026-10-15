package adapter

import (
	"context"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
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

// GetApprovedVendorID confirms userID holds permission on vendorID (AF-17:
// the owner, or staff granted it); an approved or suspended shop may still
// fulfil existing orders. A refusal is 403 permission_denied, an unknown
// answer 503.
func (c *HTTPVendorClient) GetApprovedVendorID(ctx context.Context, userID, vendorID, permission string) (string, error) {
	grant, err := (shopaccess.Client{URL: c.baseURL, Key: c.key, HTTP: c.client}).Authorize(ctx, userID, vendorID, permission)
	if err != nil {
		return "", err
	}
	if grant.Status != "approved" && grant.Status != "suspended" {
		return "", apperror.Forbidden("You must have an approved vendor account to view vendor orders")
	}
	return grant.VendorID, nil
}

// Approved is checkout's live selling check: every shop approved and
// taking new orders (AF-18: a shop on vacation answers 409
// vendor_not_accepting_orders). The versions fence order creation.
func (c *HTTPVendorClient) Approved(ctx context.Context, ids []string) (map[string]int64, error) {
	return (vendorsales.Client{URL: c.baseURL, Key: c.key}).Accepting(ctx, ids)
}
