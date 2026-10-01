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
)

type HTTPNotificationClient struct {
	baseURL    string
	serviceKey string
	client     *http.Client
}

func NewHTTPNotificationClient(baseURL, serviceKey string) *HTTPNotificationClient {
	return &HTTPNotificationClient{baseURL: baseURL, serviceKey: serviceKey, client: &http.Client{Timeout: 5 * time.Second}}
}

// Notify hands one notice to Notification, which records it and delivers
// it in the background. eventID is the notify effect's id, so a retried
// effect is recognised as the same event. A 4xx answer is a refusal (the
// effect parks); anything else unexpected is retried.
func (c *HTTPNotificationClient) Notify(ctx context.Context, eventID, userID, notifType, referenceID string) error {
	payload, err := json.Marshal(map[string]string{
		"event_id": eventID, "source": "order", "user_id": userID, "type": notifType, "reference_id": referenceID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/notifications", bytes.NewReader(payload))
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
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &apperror.Error{Code: apperror.CodeValidation, Status: resp.StatusCode, Message: fmt.Sprintf("notification refused with %d", resp.StatusCode)}
	}
	return apperror.Internal(fmt.Errorf("notification returned %d", resp.StatusCode))
}
