package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

// ShipmentEvent is a fulfillment fact for Order.
type ShipmentEvent struct {
	EventID        string    `json:"event_id"`
	ShipmentID     string    `json:"shipment_id"`
	VendorOrderID  string    `json:"vendor_order_id"`
	Type           string    `json:"type"`
	OccurredAt     time.Time `json:"occurred_at"`
	TrackingNumber *string   `json:"tracking_number,omitempty"`
}

// SendShipmentEvent tells Order a package shipped, was delivered or came
// back. Order decides what that means for the vendor order. A 4xx other
// than auth or rate limiting is Order refusing it and comes back as a
// conflict for review; anything else is retried.
func (c *HTTPOrderClient) SendShipmentEvent(ctx context.Context, e ShipmentEvent) error {
	body, err := json.Marshal(e)
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/shipment-events", bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("order service unreachable: %w", err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests:
		return apperror.Conflict(fmt.Sprintf("Order refused the %s event (status %d)", e.Type, resp.StatusCode))
	}
	return apperror.Internal(fmt.Errorf("order service returned status %d for a shipment event", resp.StatusCode))
}
