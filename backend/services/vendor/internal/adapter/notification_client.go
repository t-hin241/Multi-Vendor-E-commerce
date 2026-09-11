package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type HTTPNotificationClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPNotificationClient(baseURL string) *HTTPNotificationClient {
	return &HTTPNotificationClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

// Notify tells Notification to reach userID about an event. Vendor treats
// this as fire-and-forget — a notification failure must never roll back a
// moderation decision — so it only ever returns an error for the caller to
// log, never one that should abort anything.
func (c *HTTPNotificationClient) Notify(ctx context.Context, userID, notifType, referenceID string) error {
	payload, err := json.Marshal(map[string]string{"user_id": userID, "type": notifType, "reference_id": referenceID})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/notifications", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
