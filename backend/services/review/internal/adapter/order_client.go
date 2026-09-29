package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
)

type EligibleOrderItem struct {
	OrderItemID, VendorOrderID, ProductID, VendorID, ProductName string
	VariantLabel                                                 *string
}
type OrderGateway interface {
	ListEligible(ctx context.Context, buyerID, productID string) ([]EligibleOrderItem, error)
}
type HTTPOrderClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
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
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("order service returned status %d", resp.StatusCode))
	}
	var body struct {
		Data []EligibleOrderItem `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}
	return body.Data, nil
}
