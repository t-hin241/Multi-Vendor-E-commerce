package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"time"
)

type ReserveLine struct {
	ProductID string  `json:"product_id"`
	VariantID *string `json:"variant_id,omitempty"`
	Quantity  int64   `json:"quantity"`
}
type ReservationReceipt struct {
	OrderID     string        `json:"order_id"`
	OperationID string        `json:"operation_id"`
	Status      string        `json:"status"`
	ExpiresAt   time.Time     `json:"expires_at"`
	Items       []ReserveLine `json:"items"`
}
type HTTPInventoryClient struct {
	baseURL, key string
	client       *http.Client
}

func NewHTTPInventoryClient(baseURL, key string) *HTTPInventoryClient {
	return &HTTPInventoryClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 5 * time.Second}}
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *HTTPInventoryClient) call(ctx context.Context, method, path, id string, payload any) (*ReservationReceipt, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "inventory-"+id)
		serviceauth.SetRequestHeaders(req, c.key)
		resp, err := c.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			continue
		}
		if resp.StatusCode >= 500 {
			continue
		}
		if resp.StatusCode == 404 {
			return nil, apperror.NotFound("Reservation operation not found")
		}
		if resp.StatusCode == 409 || resp.StatusCode == 400 {
			// Keep Inventory's own message (e.g. which product is short)
			// so checkout can tell the buyer what to change.
			var envelope errorEnvelope
			_ = json.Unmarshal(data, &envelope)
			if resp.StatusCode == 400 {
				msg := envelope.Error.Message
				if msg == "" {
					msg = "Inventory rejected the reservation request"
				}
				return nil, apperror.Validation(msg)
			}
			msg := envelope.Error.Message
			if msg == "" {
				msg = "Inventory operation conflicts with its current state"
			}
			return nil, apperror.Conflict(msg)
		}
		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			return nil, apperror.Internal(fmt.Errorf("inventory returned status %d", resp.StatusCode))
		}
		var envelope struct {
			Data ReservationReceipt `json:"data"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, apperror.Internal(err)
		}
		if envelope.Data.OrderID != id || envelope.Data.OperationID != id {
			return nil, apperror.Internal(fmt.Errorf("invalid inventory operation receipt"))
		}
		return &envelope.Data, nil
	}
	return nil, apperror.Internal(fmt.Errorf("inventory operation response unavailable; retry the same operation ID"))
}
func (c *HTTPInventoryClient) Operation(ctx context.Context, id string) (*ReservationReceipt, error) {
	return c.call(ctx, http.MethodGet, "/internal/inventory/operations/"+id, id, nil)
}
func (c *HTTPInventoryClient) Reserve(ctx context.Context, id string, lines []ReserveLine) error {
	receipt, err := c.call(ctx, http.MethodPost, "/internal/inventory/reserve", id, struct {
		OrderID string        `json:"order_id"`
		Items   []ReserveLine `json:"items"`
	}{id, lines})
	if err != nil {
		return err
	}
	if receipt.Status != "held" || !receipt.ExpiresAt.After(time.Now()) {
		return apperror.Conflict("Reservation is no longer held")
	}
	return nil
}
func (c *HTTPInventoryClient) Release(ctx context.Context, id string) error {
	receipt, err := c.call(ctx, http.MethodPost, "/internal/inventory/release", id, map[string]string{"order_id": id})
	if err != nil {
		return err
	}
	if receipt.Status != "released" && receipt.Status != "expired" {
		return apperror.Conflict("Inventory has not released the operation")
	}
	return nil
}

// RestockReturn puts units of a received, inspected return back into
// available stock. Inventory makes it idempotent per return id.
func (c *HTTPInventoryClient) RestockReturn(ctx context.Context, returnID, productID string, variantID *string, quantity int64) error {
	body, err := json.Marshal(map[string]any{"return_id": returnID, "product_id": productID, "variant_id": variantID, "quantity": quantity})
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/inventory/returns", bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("inventory service unreachable: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	var envelope errorEnvelope
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusForbidden {
		return &apperror.Error{Code: apperror.CodeConflict, Status: resp.StatusCode, Message: envelope.Error.Message}
	}
	return apperror.Internal(fmt.Errorf("inventory returned status %d", resp.StatusCode))
}

func (c *HTTPInventoryClient) Commit(ctx context.Context, id string) error {
	receipt, err := c.call(ctx, http.MethodPost, "/internal/inventory/commit", id, map[string]string{"order_id": id})
	if err != nil {
		return err
	}
	if receipt.Status != "committed" {
		return apperror.Conflict("Inventory has not committed the operation")
	}
	return nil
}
