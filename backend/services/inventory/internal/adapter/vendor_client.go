// Package adapter holds Inventory's outbound integrations with other
// services. The use case depends on the Gateway interfaces declared in the
// usecase package, never on these HTTP clients directly.
package adapter

import (
	"context"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/telemetry"
)

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
		return "", apperror.Forbidden("You must have an approved vendor account to manage stock")
	}
	return grant.VendorID, nil
}
