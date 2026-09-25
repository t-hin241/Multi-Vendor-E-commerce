// Package adapter holds Order's outbound integrations with Cart, Catalog,
// Vendor and Inventory. The use case depends on the Gateway interfaces
// declared in the usecase package, never on these HTTP clients directly.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
)

type CartLine struct {
	ProductID string
	VariantID *string
	Quantity  int64
}

type HTTPCartClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPCartClient(baseURL string) *HTTPCartClient {
	return &HTTPCartClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type cartViewResponse struct {
	Data struct {
		Items []struct {
			ProductID string  `json:"product_id"`
			VariantID *string `json:"variant_id"`
			Quantity  int64   `json:"quantity"`
		} `json:"items"`
	} `json:"data"`
}

// GetItems reads the buyer's raw cart selection (product + quantity only —
// Order re-derives price and vendor from Catalog itself for the checkout
// snapshot, never trusting whatever Cart last displayed). bearerToken is
// the buyer's own access token, forwarded as-is so Cart's normal auth
// middleware applies; Order never impersonates a buyer with its own
// credentials.
func (c *HTTPCartClient) GetItems(ctx context.Context, bearerToken string) ([]CartLine, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/cart", nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("cart service returned status %d", resp.StatusCode))
	}

	var body cartViewResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	lines := make([]CartLine, 0, len(body.Data.Items))
	for _, item := range body.Data.Items {
		lines = append(lines, CartLine{ProductID: item.ProductID, VariantID: item.VariantID, Quantity: item.Quantity})
	}
	return lines, nil
}

// Clear empties the buyer's cart after a successful checkout. Failure here
// is not checkout-critical — the order already exists and its stock is
// already reserved — so the use case treats it as best-effort.
func (c *HTTPCartClient) Clear(ctx context.Context, bearerToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/cart", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cart service returned status %d", resp.StatusCode)
	}
	return nil
}
