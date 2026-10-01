package adapter

import (
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

// EligibleOrderItem is Order's proof that the buyer received this item in
// a completed vendor order (no address, payment or totals).
type EligibleOrderItem struct {
	OrderItemID   string  `json:"order_item_id"`
	VendorOrderID string  `json:"vendor_order_id"`
	ProductID     string  `json:"product_id"`
	VendorID      string  `json:"vendor_id"`
	ProductName   string  `json:"product_name"`
	VariantLabel  *string `json:"variant_label,omitempty"`
	CompletedAt   string  `json:"completed_at"`
}

type OrderGateway interface {
	ListEligible(ctx context.Context, buyerID, productID string) ([]EligibleOrderItem, error)
}

type HTTPOrderClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL, key string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 5 * time.Second}}
}

// ErrOrderUnavailable: Order did not answer, so eligibility is unknown and
// nothing is decided (the buyer can try again).
func ErrOrderUnavailable(err error) *apperror.Error {
	return &apperror.Error{Code: "service_unavailable", Message: "Purchase records are unavailable right now; please try again shortly",
		Status: http.StatusServiceUnavailable, Err: err}
}

func (c *HTTPOrderClient) ListEligible(ctx context.Context, buyerID, productID string) ([]EligibleOrderItem, error) {
	v := url.Values{"buyer_id": {buyerID}}
	if productID != "" {
		v.Set("product_id", productID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/orders/review-eligibility?"+v.Encode(), nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, ErrOrderUnavailable(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrOrderUnavailable(fmt.Errorf("order service returned status %d", resp.StatusCode))
	case resp.StatusCode != http.StatusOK:
		return nil, apperror.Internal(fmt.Errorf("order service returned status %d", resp.StatusCode))
	}
	var body struct {
		Data []EligibleOrderItem `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, apperror.Internal(fmt.Errorf("decode order eligibility: %w", err))
	}
	return body.Data, nil
}
