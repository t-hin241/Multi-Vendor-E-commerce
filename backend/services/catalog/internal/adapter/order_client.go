package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OrderGateway lets the storefront listing resolve units-sold for a batch
// of product ids without Catalog owning any order data itself.
type OrderGateway interface {
	GetQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, error)
}

type HTTPOrderClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type quantitySoldResponse struct {
	Data []struct {
		ProductID    string `json:"product_id"`
		QuantitySold int64  `json:"quantity_sold"`
	} `json:"data"`
}

// GetQuantitySold resolves units-sold for a batch of product ids in one
// call. Any id with no sales yet is simply absent from the result — the
// caller (ProductUseCase.resolveQuantitySold) falls back to its own cache
// for whatever's missing due to an actual error, and treats a product that
// was never sold as a legitimate zero.
func (c *HTTPOrderClient) GetQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, error) {
	if len(productIDs) == 0 {
		return map[string]int64{}, nil
	}

	endpoint := fmt.Sprintf("%s/internal/orders/products/quantity-sold?product_ids=%s", c.baseURL, url.QueryEscape(strings.Join(productIDs, ",")))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("order service returned status %d", resp.StatusCode)
	}

	var body quantitySoldResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(body.Data))
	for _, item := range body.Data {
		out[item.ProductID] = item.QuantitySold
	}
	return out, nil
}
