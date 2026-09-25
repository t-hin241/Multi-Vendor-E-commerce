package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
)

type ReserveLine struct {
	ProductID string  `json:"product_id"`
	VariantID *string `json:"variant_id,omitempty"`
	Quantity  int64   `json:"quantity"`
}

type HTTPInventoryClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPInventoryClient(baseURL string) *HTTPInventoryClient {
	return &HTTPInventoryClient{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type reserveRequestBody struct {
	OrderID string        `json:"order_id"`
	Items   []ReserveLine `json:"items"`
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Reserve holds stock for every line of orderID. A 409 from Inventory means
// the checkout can't be fulfilled (something is out of stock) and is
// surfaced as a Conflict, not an infrastructure failure.
func (c *HTTPInventoryClient) Reserve(ctx context.Context, orderID string, lines []ReserveLine) error {
	payload, err := json.Marshal(reserveRequestBody{OrderID: orderID, Items: lines})
	if err != nil {
		return apperror.Internal(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/inventory/reserve", bytes.NewReader(payload))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return nil
	}

	var body errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if resp.StatusCode == http.StatusConflict {
		msg := body.Error.Message
		if msg == "" {
			msg = "Not enough stock to fulfill this order"
		}
		return apperror.Conflict(msg)
	}

	return apperror.Internal(fmt.Errorf("inventory service returned status %d", resp.StatusCode))
}

// Release returns any active reservation for orderID back to available
// stock. It is safe to call even if nothing was ever reserved.
func (c *HTTPInventoryClient) Release(ctx context.Context, orderID string) error {
	return c.postOrderID(ctx, "/internal/inventory/release", orderID)
}

// Commit finalizes orderID's held reservation into a permanent stock
// decrement once payment has succeeded. It is safe to call more than once.
func (c *HTTPInventoryClient) Commit(ctx context.Context, orderID string) error {
	return c.postOrderID(ctx, "/internal/inventory/commit", orderID)
}

func (c *HTTPInventoryClient) postOrderID(ctx context.Context, path, orderID string) error {
	payload, err := json.Marshal(map[string]string{"order_id": orderID})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inventory service returned status %d", resp.StatusCode)
	}
	return nil
}
