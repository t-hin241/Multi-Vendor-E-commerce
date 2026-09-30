package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"shopee/backend/pkg/serviceauth"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
)

// OrderSnapshot is Order's own record of an order, read fresh at payment-
// intent-creation time so the amount collected is never sized by anything
// the client sent.
type OrderSnapshot struct {
	InventoryStatus      string
	ReservationExpiresAt *time.Time
	ID                   string
	BuyerID              string
	Status               string
	TotalAmount          int64
	Currency             string
}

type HTTPOrderClient struct {
	key     string
	baseURL string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL, key string) *HTTPOrderClient {
	return &HTTPOrderClient{key: key, baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type internalOrderResponseBody struct {
	Data struct {
		InventoryStatus      string     `json:"inventory_status"`
		ReservationExpiresAt *time.Time `json:"reservation_expires_at"`
		ID                   string     `json:"id"`
		BuyerID              string     `json:"buyer_id"`
		Status               string     `json:"status"`
		TotalAmount          int64      `json:"total_amount"`
		Currency             string     `json:"currency"`
	} `json:"data"`
}

func (c *HTTPOrderClient) GetOrder(ctx context.Context, orderID string) (*OrderSnapshot, error) {
	endpoint := fmt.Sprintf("%s/internal/orders/%s", c.baseURL, url.PathEscape(orderID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, apperror.NotFound("Order not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("order service returned status %d", resp.StatusCode))
	}

	var body internalOrderResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	return &OrderSnapshot{
		InventoryStatus: body.Data.InventoryStatus, ReservationExpiresAt: body.Data.ReservationExpiresAt, ID: body.Data.ID, BuyerID: body.Data.BuyerID, Status: body.Data.Status,
		TotalAmount: body.Data.TotalAmount, Currency: body.Data.Currency,
	}, nil
}

// Capture is the verified payment Order checks against its own snapshot.
type Capture struct {
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// MarkPaid reports a captured payment back to Order. Order — not Payment —
// decides whether the resulting lifecycle transition is valid, and answers
// 409 when the capture cannot pay the order (it then records it for a
// refund review).
func (c *HTTPOrderClient) MarkPaid(ctx context.Context, orderID string, capture Capture) error {
	endpoint := fmt.Sprintf("%s/internal/orders/%s/mark-paid", c.baseURL, url.PathEscape(orderID))
	return c.post(ctx, endpoint, capture, "marking order paid")
}

// ReportRefund delivers a resolved refund's outcome to Order. Any 4xx
// other than auth or rate limiting is Order refusing the outcome and comes
// back as a conflict for review; everything else is retryable.
func (c *HTTPOrderClient) ReportRefund(ctx context.Context, outcome domain.RefundOutcome) error {
	return c.post(ctx, c.baseURL+"/internal/refund-events", outcome, "reporting refund outcome")
}

func (c *HTTPOrderClient) post(ctx context.Context, endpoint string, payload any, action string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests:
		return apperror.Conflict("Order rejected payment outcome; reconciliation required")
	default:
		return apperror.Internal(fmt.Errorf("order service returned status %d %s", resp.StatusCode, action))
	}
}

// MarkPaymentFailed reports a failed payment back to Order, which cancels
// the order and releases its stock hold.
func (c *HTTPOrderClient) MarkPaymentFailed(ctx context.Context, orderID, reason string) error {
	endpoint := fmt.Sprintf("%s/internal/orders/%s/mark-payment-failed", c.baseURL, url.PathEscape(orderID))

	body, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return apperror.Internal(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return apperror.Conflict("Order rejected payment outcome; reconciliation required")
	}
	if resp.StatusCode != http.StatusOK {
		return apperror.Internal(fmt.Errorf("order service returned status %d marking payment failed", resp.StatusCode))
	}
	return nil
}

// HeldVendorOrders asks Order which vendor orders must not be paid out yet
// (an open return or refund). An error means the answer is unknown and the
// caller must not pay.
func (c *HTTPOrderClient) HeldVendorOrders(ctx context.Context, vendorOrderIDs []string) (map[string]string, error) {
	held := map[string]string{}
	if len(vendorOrderIDs) == 0 {
		return held, nil
	}
	body, err := json.Marshal(map[string][]string{"vendor_order_ids": vendorOrderIDs})
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/settlements/holds", bytes.NewReader(body))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(fmt.Errorf("order service unreachable: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("order service returned status %d for settlement holds", resp.StatusCode))
	}
	var envelope struct {
		Data struct {
			Held []struct {
				VendorOrderID string `json:"vendor_order_id"`
				Reason        string `json:"reason"`
			} `json:"held"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, apperror.Internal(err)
	}
	for _, h := range envelope.Data.Held {
		held[h.VendorOrderID] = h.Reason
	}
	return held, nil
}
