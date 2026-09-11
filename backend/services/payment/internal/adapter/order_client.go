package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// OrderSnapshot is Order's own record of an order, read fresh at payment-
// intent-creation time so the amount collected is never sized by anything
// the client sent.
type OrderSnapshot struct {
	ID          string
	BuyerID     string
	Status      string
	TotalAmount int64
	Currency    string
}

type HTTPOrderClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type internalOrderResponseBody struct {
	Data struct {
		ID          string `json:"id"`
		BuyerID     string `json:"buyer_id"`
		Status      string `json:"status"`
		TotalAmount int64  `json:"total_amount"`
		Currency    string `json:"currency"`
	} `json:"data"`
}

func (c *HTTPOrderClient) GetOrder(ctx context.Context, orderID string) (*OrderSnapshot, error) {
	endpoint := fmt.Sprintf("%s/internal/orders/%s", c.baseURL, url.PathEscape(orderID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}

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
		ID: body.Data.ID, BuyerID: body.Data.BuyerID, Status: body.Data.Status,
		TotalAmount: body.Data.TotalAmount, Currency: body.Data.Currency,
	}, nil
}

// MarkPaid reports a captured payment back to Order. Order — not Payment —
// decides whether the resulting lifecycle transition is valid.
func (c *HTTPOrderClient) MarkPaid(ctx context.Context, orderID string) error {
	endpoint := fmt.Sprintf("%s/internal/orders/%s/mark-paid", c.baseURL, url.PathEscape(orderID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return apperror.Internal(err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return apperror.Internal(fmt.Errorf("order service returned status %d marking order paid", resp.StatusCode))
	}
	return nil
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

	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return apperror.Internal(fmt.Errorf("order service returned status %d marking payment failed", resp.StatusCode))
	}
	return nil
}
