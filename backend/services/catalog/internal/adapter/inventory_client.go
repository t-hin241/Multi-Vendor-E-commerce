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

// InventoryGateway lets the public product-detail page resolve current
// stock for a batch of variant ids without Catalog owning any inventory
// data itself.
type InventoryGateway interface {
	GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error)
}

type HTTPInventoryClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPInventoryClient(baseURL string) *HTTPInventoryClient {
	return &HTTPInventoryClient{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

type variantStockResponse struct {
	Data []struct {
		VariantID         string `json:"variant_id"`
		AvailableQuantity int64  `json:"available_quantity"`
	} `json:"data"`
}

// GetVariantStock resolves current stock for a batch of variant ids in one
// call. A variant with no inventory row yet is simply absent from the
// result — the caller (ProductUseCase.resolveVariantStock) treats that as a
// legitimate zero (not yet stocked), same asymmetry already used for
// units-sold, and only falls back to its own cache on an actual call
// failure.
func (c *HTTPInventoryClient) GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error) {
	if len(variantIDs) == 0 {
		return map[string]int64{}, nil
	}

	endpoint := fmt.Sprintf("%s/internal/inventory/variants/stock?variant_ids=%s", c.baseURL, url.QueryEscape(strings.Join(variantIDs, ",")))

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
		return nil, fmt.Errorf("inventory service returned status %d", resp.StatusCode)
	}

	var body variantStockResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(body.Data))
	for _, item := range body.Data {
		out[item.VariantID] = item.AvailableQuantity
	}
	return out, nil
}
