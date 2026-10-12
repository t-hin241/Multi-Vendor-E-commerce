package adapter

import (
	"context"
	"shopee/backend/pkg/purchasequote"
)

func (c *HTTPCatalogClient) PurchaseQuotes(ctx context.Context, lines []purchasequote.Input) (*purchasequote.Quote, error) {
	return (purchasequote.Client{URL: c.baseURL, Key: c.key}).PurchaseQuotes(ctx, lines)
}
