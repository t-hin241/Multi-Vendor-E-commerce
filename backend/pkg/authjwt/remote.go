package authjwt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// RemoteVerifier fails closed and deliberately does not cache revocation state.
func RemoteVerifier(baseURL, key string) func(context.Context, *Claims) error {
	client := &http.Client{Timeout: 2 * time.Second}
	return func(ctx context.Context, claims *Claims) error {
		if claims.SessionID == "" {
			return ErrInvalidToken
		}
		body, err := json.Marshal(map[string]string{"user_id": claims.UserID, "role": claims.Role, "session_id": claims.SessionID})
		if err != nil {
			return ErrVerificationUnavailable
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/sessions/verify", bytes.NewReader(body))
		if err != nil {
			return ErrVerificationUnavailable
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Identity-Service-Key", key)
		res, err := client.Do(req)
		if err != nil {
			return ErrVerificationUnavailable
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNoContent {
			return nil
		}
		if res.StatusCode == http.StatusUnauthorized {
			return ErrInvalidToken
		}
		return ErrVerificationUnavailable
	}
}
