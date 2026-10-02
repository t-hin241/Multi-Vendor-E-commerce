package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

// IdentityGateway resolves a review author's name, which Review masks
// before storing (domain.MaskName); no credential or contact information
// is read.
type IdentityGateway interface {
	DisplayName(ctx context.Context, userID string) (string, error)
}

type HTTPIdentityClient struct {
	baseURL    string
	serviceKey string
	client     *http.Client
}

func NewHTTPIdentityClient(baseURL, serviceKey string) *HTTPIdentityClient {
	return &HTTPIdentityClient{serviceKey: serviceKey, baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 5 * time.Second}}
}

func (c *HTTPIdentityClient) DisplayName(ctx context.Context, userID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/users/"+url.PathEscape(userID), nil)
	if err != nil {
		return "", apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.serviceKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", apperror.Internal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", apperror.Internal(fmt.Errorf("identity service returned status %d", resp.StatusCode))
	}
	var body struct {
		Data struct {
			FullName string `json:"full_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", apperror.Internal(err)
	}
	return strings.TrimSpace(body.Data.FullName), nil
}
