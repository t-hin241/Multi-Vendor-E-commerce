package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// HTTPNotificationClient reports Payment's notices to Notification over
// HTTP while the event bus is off (PW-045); with the bus they are events.
// The outbox row id is the event id, so a retry is the same notice.
type HTTPNotificationClient struct {
	baseURL    string
	serviceKey string
	client     *http.Client
}

func NewHTTPNotificationClient(baseURL, serviceKey string) *HTTPNotificationClient {
	return &HTTPNotificationClient{baseURL: baseURL, serviceKey: serviceKey, client: telemetry.NewHTTPClient(5 * time.Second)}
}

// NotifyPayout tells a shop a payout transfer succeeded or failed (AF-08).
func (c *HTTPNotificationClient) NotifyPayout(ctx context.Context, eventID, vendorID, payoutItemID, outcome string) error {
	return c.post(ctx, "/internal/vendor-action-notices", map[string]string{"event_id": eventID, "source": "payment",
		"vendor_id": vendorID, "payout_id": payoutItemID, "outcome": outcome})
}

// Notify hands a buyer notice (PW-009) to Notification.
func (c *HTTPNotificationClient) Notify(ctx context.Context, eventID, userID, notifType, referenceID string) error {
	return c.post(ctx, "/internal/notifications", map[string]string{"event_id": eventID, "source": "payment",
		"user_id": userID, "type": notifType, "reference_id": referenceID})
}

func (c *HTTPNotificationClient) post(ctx context.Context, path string, body map[string]string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.serviceKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("notification unreachable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return apperror.Internal(fmt.Errorf("notification returned %d", resp.StatusCode))
}
