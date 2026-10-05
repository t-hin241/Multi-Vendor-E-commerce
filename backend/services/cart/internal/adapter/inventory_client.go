package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// HTTPInventoryClient reads current stock so the cart can warn about
// out-of-stock lines. It is display-only: Inventory's reservation at
// checkout is the only stock guarantee.
type HTTPInventoryClient struct {
	key     string
	baseURL string
	client  *http.Client
}

func NewHTTPInventoryClient(baseURL, key string) *HTTPInventoryClient {
	return &HTTPInventoryClient{key: key, baseURL: baseURL, client: telemetry.NewHTTPClient(3 * time.Second)}
}

// maxVariantStockBatch mirrors Inventory's own per-request id limit.
const maxVariantStockBatch = 100

// GetVariantStock returns available quantity per variant id in one call per
// 100 ids. A variant with no inventory row is absent from the map.
func (c *HTTPInventoryClient) GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(variantIDs))
	for start := 0; start < len(variantIDs); start += maxVariantStockBatch {
		end := min(start+maxVariantStockBatch, len(variantIDs))
		endpoint := fmt.Sprintf("%s/internal/inventory/variants/stock?variant_ids=%s", c.baseURL, url.QueryEscape(strings.Join(variantIDs[start:end], ",")))
		var body struct {
			Data []struct {
				VariantID         string `json:"variant_id"`
				AvailableQuantity int64  `json:"available_quantity"`
			} `json:"data"`
		}
		if err := c.get(ctx, endpoint, &body); err != nil {
			return nil, err
		}
		for _, item := range body.Data {
			out[item.VariantID] = item.AvailableQuantity
		}
	}
	return out, nil
}

// GetProductStock returns available quantity of a product without
// variants; exists is false when Inventory has no row for it.
func (c *HTTPInventoryClient) GetProductStock(ctx context.Context, productID string) (int64, bool, error) {
	endpoint := fmt.Sprintf("%s/internal/inventory/products/%s/stock", c.baseURL, url.PathEscape(productID))
	var body struct {
		Data struct {
			AvailableQuantity int64 `json:"available_quantity"`
			Exists            bool  `json:"exists"`
		} `json:"data"`
	}
	if err := c.get(ctx, endpoint, &body); err != nil {
		return 0, false, err
	}
	return body.Data.AvailableQuantity, body.Data.Exists, nil
}

func (c *HTTPInventoryClient) get(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inventory service returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
