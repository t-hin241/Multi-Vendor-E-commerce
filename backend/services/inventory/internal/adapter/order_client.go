package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/inventory/internal/domain"
	"time"
)

type OrderClient struct{ URL, Key string }

func (c OrderClient) Status(ctx context.Context, id string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/internal/orders/"+id+"/inventory-status", nil)
	if err != nil {
		return "", err
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("order status unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "missing", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("order status returned %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Data.Status, nil
}
func (c OrderClient) Publish(ctx context.Context, e domain.OutboxEvent) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/internal/inventory-events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", e.ID)
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("order event delivery unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("order event returned %d", resp.StatusCode)
	}
	return nil
}

// BusOrderEvents keeps OrderClient's status reads and publishes the
// reservation outbox to the event bus (inventory.reservation_expired).
type BusOrderEvents struct {
	OrderClient
	Bus *eventbus.Bus
}

func (c BusOrderEvents) Publish(ctx context.Context, e domain.OutboxEvent) error {
	env, err := events.ReservationExpiredEvent(events.ReservationExpiry{ID: e.ID, OrderID: e.OrderID, Type: e.Type})
	if err != nil {
		return err
	}
	return c.Bus.Publish(ctx, env.WithCorrelation(e.ID))
}

// BusStockInvalidator publishes inventory.stock_changed for a batch of
// variants (Catalog drops their cached stock).
func BusStockInvalidator(bus *eventbus.Bus) func(context.Context, []string) error {
	return func(ctx context.Context, ids []string) error {
		env, err := events.StockChangedEvent(ids)
		if err != nil {
			return err
		}
		return bus.Publish(ctx, env.WithCorrelation(""))
	}
}
