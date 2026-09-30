package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/shipment/internal/domain"
)

// VendorOrderSnapshot is Order's own record of a vendor sub-order, read
// fresh so Shipment never trusts a vendor_id or status the client sent.
// BuyerID through PackageWeightGrams back the vendor-triggered
// CreateOrGet fallback. Fulfillable is Order's decision that the vendor may
// ship (payment verified and stock committed); Quote is the shipping fee
// Order snapshotted at checkout, when it has one.
type VendorOrderSnapshot struct {
	ID          string
	OrderID     string
	VendorID    string
	Status      string
	Fulfillable bool

	BuyerID            string
	RecipientName      string
	Phone              string
	Province           string
	District           string
	Ward               string
	StreetAddress      string
	PackageWeightGrams int64
	Quote              *domain.QuotedFee
}

type HTTPOrderClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPOrderClient(baseURL, key string) *HTTPOrderClient {
	return &HTTPOrderClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 10 * time.Second}}
}

type internalVendorOrderResponseBody struct {
	Data struct {
		ID          string `json:"id"`
		OrderID     string `json:"order_id"`
		VendorID    string `json:"vendor_id"`
		Status      string `json:"status"`
		Fulfillable bool   `json:"fulfillable"`

		BuyerID            string `json:"buyer_id"`
		RecipientName      string `json:"recipient_name"`
		Phone              string `json:"phone"`
		Province           string `json:"province"`
		District           string `json:"district"`
		Ward               string `json:"ward"`
		StreetAddress      string `json:"street_address"`
		PackageWeightGrams int64  `json:"package_weight_grams"`

		ShippingFeeAmount *int64  `json:"shipping_fee_amount"`
		ShippingCarrierID *string `json:"shipping_carrier_id"`
		ShippingZoneID    *string `json:"shipping_zone_id"`
		ShippingFeeRuleID *string `json:"shipping_fee_rule_id"`
	} `json:"data"`
}

// GetVendorOrder lets Shipment verify that the vendor calling it actually
// owns the vendor sub-order they're creating/advancing a shipment for, and
// that Order considers it fulfillable.
func (c *HTTPOrderClient) GetVendorOrder(ctx context.Context, vendorOrderID string) (*VendorOrderSnapshot, error) {
	endpoint := fmt.Sprintf("%s/internal/vendor-orders/%s", c.baseURL, url.PathEscape(vendorOrderID))

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

	var body internalVendorOrderResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}

	d := body.Data
	snapshot := &VendorOrderSnapshot{
		ID: d.ID, OrderID: d.OrderID, VendorID: d.VendorID, Status: d.Status, Fulfillable: d.Fulfillable,
		BuyerID: d.BuyerID, RecipientName: d.RecipientName, Phone: d.Phone,
		Province: d.Province, District: d.District, Ward: d.Ward,
		StreetAddress: d.StreetAddress, PackageWeightGrams: d.PackageWeightGrams,
	}
	if d.ShippingFeeAmount != nil && d.ShippingCarrierID != nil && d.ShippingZoneID != nil && d.ShippingFeeRuleID != nil {
		snapshot.Quote = &domain.QuotedFee{FeeAmount: *d.ShippingFeeAmount, CarrierID: *d.ShippingCarrierID, ZoneID: *d.ShippingZoneID, FeeRuleID: *d.ShippingFeeRuleID}
	}
	return snapshot, nil
}
