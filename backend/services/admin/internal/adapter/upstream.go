// Package adapter reads the domain services' admin APIs on behalf of the
// signed-in admin. It forwards the admin's own access token, so every
// service still checks the caller itself; Admin never uses a service key
// and only sends GET requests to fixed paths.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"shopee/backend/pkg/middleware"
)

// ErrUnavailable means the service could not be read (down, slow, or an
// unexpected answer); the caller shows it as unavailable, never as zero.
var ErrUnavailable = errors.New("service unavailable")

// Refusal is a 4xx answer from a service (for example the admin's role
// was revoked, or the filter was rejected).
type Refusal struct {
	Status  int
	Code    string
	Message string
}

func (r *Refusal) Error() string { return fmt.Sprintf("refused with %d: %s", r.Status, r.Code) }

type Client struct {
	HTTP *http.Client
}

// Get calls baseURL+path with query, forwarding the admin's Authorization
// header and the request id, and returns the "data" member of the reply.
func (c Client) Get(ctx context.Context, baseURL, path string, query url.Values, authorization string) (json.RawMessage, error) {
	target := baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request", ErrUnavailable)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	if id := middleware.RequestIDFromContext(ctx); id != "" {
		req.Header.Set(middleware.RequestIDHeader, id)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, transportFailure(ctx))
	}
	defer resp.Body.Close()
	var body struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: status %d with an unreadable body", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && body.Error != nil {
		return nil, &Refusal{Status: resp.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
	}
	if resp.StatusCode != http.StatusOK || body.Data == nil {
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	return body.Data, nil
}

func transportFailure(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out"
	}
	return "unreachable"
}
