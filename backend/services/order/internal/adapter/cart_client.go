// Package adapter holds Order's outbound integrations with Cart, Catalog,
// Vendor and Inventory. The use case depends on the Gateway interfaces
// declared in the usecase package, never on these HTTP clients directly.
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

// CartLine is one snapshotted cart line. LineID identifies the exact line
// instance to consume after the order exists; SeenPrice* is the price the
// buyer last accepted in the cart (display reference, never a final price).
type CartLine struct {
	LineID          string
	ProductID       string
	VariantID       *string
	Quantity        int64
	SeenPriceAmount *int64
	SeenCurrency    *string
}

// CartSnapshot is Cart's frozen view of the buyer's cart for one checkout
// operation.
type CartSnapshot struct {
	OperationID string
	CartVersion int64
	Lines       []CartLine
}

// CartConsumeLine is a purchased quantity of one snapshotted line.
type CartConsumeLine struct {
	LineID   string `json:"line_id"`
	Quantity int64  `json:"quantity"`
}

// HTTPCartClient calls Cart's internal checkout contract with Order's
// service identity. No buyer access token is forwarded or stored, so the
// consume retry worker can run long after the buyer's request ended.
type HTTPCartClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPCartClient(baseURL, key string) *HTTPCartClient {
	return &HTTPCartClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 5 * time.Second}}
}

type snapshotEnvelope struct {
	Data struct {
		OperationID string `json:"operation_id"`
		CartVersion int64  `json:"cart_version"`
		Lines       []struct {
			LineID          string  `json:"line_id"`
			ProductID       string  `json:"product_id"`
			VariantID       *string `json:"variant_id"`
			Quantity        int64   `json:"quantity"`
			SeenPriceAmount *int64  `json:"seen_price_amount"`
			SeenCurrency    *string `json:"seen_currency"`
		} `json:"lines"`
	} `json:"data"`
}

// Snapshot freezes the buyer's cart under operationID. Repeating the call
// with the same operationID returns the same snapshot. expectedVersion, when
// given, makes Cart reject a cart the buyer has changed since they last
// reviewed it (409 cart_changed).
func (c *HTTPCartClient) Snapshot(ctx context.Context, buyerID, operationID string, expectedVersion *int64) (*CartSnapshot, error) {
	payload := map[string]any{"operation_id": operationID}
	if expectedVersion != nil {
		payload["expected_version"] = *expectedVersion
	}
	var body snapshotEnvelope
	if err := c.post(ctx, "/internal/carts/"+url.PathEscape(buyerID)+"/checkout-snapshots", payload, &body); err != nil {
		return nil, err
	}
	snap := &CartSnapshot{OperationID: body.Data.OperationID, CartVersion: body.Data.CartVersion, Lines: make([]CartLine, 0, len(body.Data.Lines))}
	for _, l := range body.Data.Lines {
		snap.Lines = append(snap.Lines, CartLine{LineID: l.LineID, ProductID: l.ProductID, VariantID: l.VariantID,
			Quantity: l.Quantity, SeenPriceAmount: l.SeenPriceAmount, SeenCurrency: l.SeenCurrency})
	}
	return snap, nil
}

// Consume removes the purchased units of the snapshotted lines from the
// buyer's cart. Cart makes it idempotent per operation.
func (c *HTTPCartClient) Consume(ctx context.Context, buyerID, operationID string, lines []CartConsumeLine) error {
	path := "/internal/carts/" + url.PathEscape(buyerID) + "/checkout-snapshots/" + url.PathEscape(operationID) + "/consume"
	return c.post(ctx, path, map[string]any{"lines": lines}, nil)
}

// post returns an *apperror.Error carrying Cart's own status/code/message
// for a 4xx answer (so cart_changed or "cart is empty" reach the buyer as
// is), and an internal error for transport failures and 5xx.
func (c *HTTPCartClient) post(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)

	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("cart service unreachable: %w", err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return apperror.Internal(fmt.Errorf("read cart response: %w", err))
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Error.Code == "" {
			envelope.Error.Code = string(apperror.CodeConflict)
		}
		if envelope.Error.Message == "" {
			envelope.Error.Message = "Your cart could not be used for checkout; please review it"
		}
		// A 401/403 here is Order's own service identity being refused —
		// never something the buyer can fix.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return apperror.Internal(fmt.Errorf("cart rejected order service identity: status %d", resp.StatusCode))
		}
		return &apperror.Error{Code: apperror.Code(envelope.Error.Code), Message: envelope.Error.Message, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return apperror.Internal(fmt.Errorf("cart service returned status %d", resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return apperror.Internal(fmt.Errorf("decode cart response: %w", err))
	}
	return nil
}
