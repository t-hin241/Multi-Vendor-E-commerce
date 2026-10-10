package identityclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

type Client struct{ URL, Key string }

func (c Client) RequireRole(ctx context.Context, userID, role string) error {
	if userID == "" {
		return apperror.Forbidden("Authentication required")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/internal/users/"+url.PathEscape(userID), nil)
	if err != nil {
		return apperror.Internal(fmt.Errorf("identity role request invalid"))
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := telemetry.NewHTTPClient(2 * time.Second).Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("identity role verification unavailable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return apperror.Forbidden("Active account with required role needed")
	}
	if resp.StatusCode != 200 {
		return apperror.Internal(fmt.Errorf("identity role verification failed"))
	}
	var body struct {
		Data struct {
			ID, Role string
			Active   bool `json:"is_active"`
		}
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&body) != nil {
		return apperror.Internal(fmt.Errorf("invalid identity response"))
	}
	if body.Data.ID != userID || body.Data.Role != role || !body.Data.Active {
		return apperror.Forbidden("Active account with required role needed")
	}
	return nil
}

// Account is what Identity confirms about a user for a service decision.
type Account struct {
	ID, Email, Role string
	Active          bool
	// EmailVerified (PW-022): the account proved it owns Email.
	EmailVerified bool
}

// Account reads one account. A missing account is Forbidden; an
// unreachable or unreadable Identity is an internal error (fail closed).
func (c Client) Account(ctx context.Context, userID string) (Account, error) {
	if userID == "" {
		return Account{}, apperror.Forbidden("Authentication required")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/internal/users/"+url.PathEscape(userID), nil)
	if err != nil {
		return Account{}, apperror.Internal(fmt.Errorf("identity account request invalid"))
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := telemetry.NewHTTPClient(2 * time.Second).Do(req)
	if err != nil {
		return Account{}, apperror.Internal(fmt.Errorf("identity account lookup unavailable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return Account{}, apperror.Forbidden("Active account needed")
	}
	if resp.StatusCode != 200 {
		return Account{}, apperror.Internal(fmt.Errorf("identity account lookup failed"))
	}
	var body struct {
		Data struct {
			ID, Email, Role string
			Active          bool `json:"is_active"`
			EmailVerified   bool `json:"email_verified"`
		}
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&body) != nil || body.Data.ID != userID {
		return Account{}, apperror.Internal(fmt.Errorf("invalid identity response"))
	}
	return Account{ID: body.Data.ID, Email: body.Data.Email, Role: body.Data.Role, Active: body.Data.Active, EmailVerified: body.Data.EmailVerified}, nil
}
