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
