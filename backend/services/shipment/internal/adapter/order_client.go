package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
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
	return &HTTPOrderClient{baseURL: baseURL, key: key, client: telemetry.NewHTTPClient(10 * time.Second)}
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

// ClaimHandover claims the fulfillment grant of a vendor order before the
// package is handed over (AF-03). 409 from Order (cancellation pending,
// not fulfillable) is a refusal; anything else is retried by the caller.
func (c *HTTPOrderClient) ClaimHandover(ctx context.Context, vendorOrderID, shipmentID string) error {
	endpoint := fmt.Sprintf("%s/internal/orders/fulfillment-grants/%s/claims", c.baseURL, url.PathEscape(vendorOrderID))
	body, err := json.Marshal(map[string]string{"shipment_id": shipmentID})
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return apperror.NotFound("Order not found")
	case http.StatusConflict:
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&envelope)
		msg := envelope.Error.Message
		if msg == "" {
			msg = "Order does not allow this package to ship"
		}
		code := apperror.Code(envelope.Error.Code)
		if code == "" {
			code = apperror.CodeConflict
		}
		return &apperror.Error{Code: code, Status: http.StatusConflict, Message: msg}
	}
	return apperror.Internal(fmt.Errorf("order service returned status %d", resp.StatusCode))
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
