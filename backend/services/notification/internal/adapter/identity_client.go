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

type UserSnapshot struct {
	ID       string
	Email    string
	FullName string
}

type HTTPIdentityClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPIdentityClient(baseURL string) *HTTPIdentityClient {
	return &HTTPIdentityClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type internalUserResponseBody struct {
	Data struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		FullName string `json:"full_name"`
	} `json:"data"`
}

// GetUser resolves a user id to the address/name Notification sends to,
// without owning any account data itself.
func (c *HTTPIdentityClient) GetUser(ctx context.Context, userID string) (*UserSnapshot, error) {
	endpoint := fmt.Sprintf("%s/internal/users/%s", c.baseURL, url.PathEscape(userID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperror.NotFound("User not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("identity service returned status %d", resp.StatusCode))
	}

	var body internalUserResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	return &UserSnapshot{ID: body.Data.ID, Email: body.Data.Email, FullName: body.Data.FullName}, nil
}
