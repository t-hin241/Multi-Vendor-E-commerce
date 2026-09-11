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

// VendorOrderSnapshot is Order's own record of a vendor sub-order, read
// fresh so Shipment never trusts a vendor_id or status the client sent.
// BuyerID through PackageWeightGrams back the vendor-triggered
// CreateOrGet fallback: Order recomputes the same destination/weight
// figures it used to quote the shipment fee at checkout time.
type VendorOrderSnapshot struct {
	ID       string
	OrderID  string
	VendorID string
	Status   string

	BuyerID            string
	RecipientName      string
	Phone              string
	Province           string
	District           string
	Ward               string
	StreetAddress      string
	PackageWeightGrams int64
}

type HTTPOrderClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type internalVendorOrderResponseBody struct {
	Data struct {
		ID       string `json:"id"`
		OrderID  string `json:"order_id"`
		VendorID string `json:"vendor_id"`
		Status   string `json:"status"`

		BuyerID            string `json:"buyer_id"`
		RecipientName      string `json:"recipient_name"`
		Phone              string `json:"phone"`
		Province           string `json:"province"`
		District           string `json:"district"`
		Ward               string `json:"ward"`
		StreetAddress      string `json:"street_address"`
		PackageWeightGrams int64  `json:"package_weight_grams"`
	} `json:"data"`
}

// GetVendorOrder lets Shipment verify that the vendor calling it actually
// owns the vendor sub-order they're creating/advancing a shipment for, and
// that it's far enough along (paid or beyond) to ship.
func (c *HTTPOrderClient) GetVendorOrder(ctx context.Context, vendorOrderID string) (*VendorOrderSnapshot, error) {
	endpoint := fmt.Sprintf("%s/internal/vendor-orders/%s", c.baseURL, url.PathEscape(vendorOrderID))

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

	var body internalVendorOrderResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	return &VendorOrderSnapshot{
		ID: body.Data.ID, OrderID: body.Data.OrderID, VendorID: body.Data.VendorID, Status: body.Data.Status,
		BuyerID: body.Data.BuyerID, RecipientName: body.Data.RecipientName, Phone: body.Data.Phone,
		Province: body.Data.Province, District: body.Data.District, Ward: body.Data.Ward,
		StreetAddress: body.Data.StreetAddress, PackageWeightGrams: body.Data.PackageWeightGrams,
	}, nil
}
