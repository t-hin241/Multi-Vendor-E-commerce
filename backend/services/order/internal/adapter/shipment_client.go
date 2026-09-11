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
)

// CreateShipmentInput carries everything Order already resolved at
// checkout time — the buyer's chosen destination and the vendor sub-
// order's total package weight — so Shipment can quote and persist a fee
// without a callback.
type CreateShipmentInput struct {
	VendorOrderID      string `json:"vendor_order_id"`
	VendorID           string `json:"vendor_id"`
	BuyerID            string `json:"buyer_id"`
	PackageWeightGrams int64  `json:"package_weight_grams"`
	RecipientName      string `json:"recipient_name"`
	Phone              string `json:"phone"`
	Province           string `json:"province"`
	District           string `json:"district"`
	Ward               string `json:"ward"`
	StreetAddress      string `json:"street_address"`
}

type HTTPShipmentClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPShipmentClient(baseURL string) *HTTPShipmentClient {
	return &HTTPShipmentClient{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

type internalShipmentResponse struct {
	Data struct {
		ID        string `json:"id"`
		FeeAmount int64  `json:"fee_amount"`
	} `json:"data"`
}

// CreateShipment is called once per vendor sub-order, right after checkout
// persists the order and reserves inventory. It is best-effort from the
// caller's point of view — see OrderUseCase.Checkout — since a briefly
// unreachable Shipment must never block or roll back an otherwise-valid
// checkout; the vendor's own manual "create shipment" fallback stays
// available if this call fails.
func (c *HTTPShipmentClient) CreateShipment(ctx context.Context, in CreateShipmentInput) (shipmentID string, feeAmount int64, err error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return "", 0, apperror.Internal(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/shipments", bytes.NewReader(payload))
	if err != nil {
		return "", 0, apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, apperror.Internal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var body errorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&body)
		msg := body.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("shipment service returned status %d", resp.StatusCode)
		}
		return "", 0, apperror.Internal(fmt.Errorf("%s", msg))
	}

	var body internalShipmentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, apperror.Internal(err)
	}
	return body.Data.ID, body.Data.FeeAmount, nil
}

// CancelForVendorOrder voids a shipment when its vendor order is cancelled
// or refunded. Also best-effort: a failure here is logged, never
// propagated — see OrderUseCase.cancel.
func (c *HTTPShipmentClient) CancelForVendorOrder(ctx context.Context, vendorOrderID string) error {
	endpoint := fmt.Sprintf("%s/internal/shipments/by-vendor-order/%s/cancel", c.baseURL, url.PathEscape(vendorOrderID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shipment service returned status %d", resp.StatusCode)
	}
	return nil
}
