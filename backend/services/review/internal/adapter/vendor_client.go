package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

type VendorGateway interface {
	EnsureOwnedApproved(ctx context.Context, userID, vendorID string) error
}
type HTTPVendorClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL, key string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 5 * time.Second}}
}
func (c *HTTPVendorClient) EnsureOwnedApproved(ctx context.Context, userID, vendorID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/internal/vendors/%s/owned-by/%s", c.baseURL, url.PathEscape(vendorID), url.PathEscape(userID)), nil)
	if err != nil {
		return apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return apperror.Forbidden("You do not own this shop")
	}
	if resp.StatusCode != http.StatusOK {
		return apperror.Internal(fmt.Errorf("vendor service returned status %d", resp.StatusCode))
	}
	return nil
}
