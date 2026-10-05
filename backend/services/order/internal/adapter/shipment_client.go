package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/order/internal/domain"
)

// CreateShipmentInput opens the shipment for a paid vendor order. Quote is
// the fee snapshotted at checkout, so Shipment keeps what the buyer paid.
type CreateShipmentInput struct {
	VendorOrderID      string        `json:"vendor_order_id"`
	VendorID           string        `json:"vendor_id"`
	BuyerID            string        `json:"buyer_id"`
	PackageWeightGrams int64         `json:"package_weight_grams"`
	RecipientName      string        `json:"recipient_name"`
	Phone              string        `json:"phone"`
	Province           string        `json:"province"`
	District           string        `json:"district"`
	Ward               string        `json:"ward"`
	StreetAddress      string        `json:"street_address"`
	Quote              *QuotedFeeRef `json:"quote,omitempty"`
}

type QuotedFeeRef struct {
	FeeAmount int64  `json:"fee_amount"`
	CarrierID string `json:"carrier_id"`
	ZoneID    string `json:"zone_id"`
	FeeRuleID string `json:"fee_rule_id"`
}

// HTTPShipmentClient calls Shipment's service-authenticated internal API.
type HTTPShipmentClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPShipmentClient(baseURL, key string) *HTTPShipmentClient {
	return &HTTPShipmentClient{baseURL: baseURL, key: key, client: telemetry.NewHTTPClient(5 * time.Second)}
}

func (c *HTTPShipmentClient) post(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return apperror.Internal(fmt.Errorf("shipment service unreachable: %w", err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return apperror.Internal(err)
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusConflict {
		var envelope errorEnvelope
		_ = json.Unmarshal(data, &envelope)
		msg := envelope.Error.Message
		if msg == "" {
			msg = "Shipping is not available for this order"
		}
		return domain.ShippingUnavailable(msg)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return apperror.Internal(fmt.Errorf("shipment service returned status %d", resp.StatusCode))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// Quote prices one vendor's package. A shop/destination Shipment cannot
// serve comes back as shipping_unavailable; it is never priced at zero.
func (c *HTTPShipmentClient) Quote(ctx context.Context, vendorID, province string, weightGrams int64) (*domain.ShippingQuote, error) {
	var body struct {
		Data struct {
			VendorID           string    `json:"vendor_id"`
			FeeAmount          int64     `json:"fee_amount"`
			Currency           string    `json:"currency"`
			CarrierID          string    `json:"carrier_id"`
			ZoneID             string    `json:"zone_id"`
			FeeRuleID          string    `json:"fee_rule_id"`
			FeeRuleVersion     int       `json:"fee_rule_version"`
			PackageWeightGrams int64     `json:"package_weight_grams"`
			QuotedAt           time.Time `json:"quoted_at"`
			ExpiresAt          time.Time `json:"expires_at"`
		} `json:"data"`
	}
	payload := map[string]any{"vendor_id": vendorID, "province": province, "package_weight_grams": weightGrams}
	if err := c.post(ctx, "/internal/shipments/quotes", payload, &body); err != nil {
		return nil, err
	}
	d := body.Data
	if d.Currency == "" || d.FeeRuleID == "" || d.FeeAmount < 0 {
		return nil, apperror.Internal(fmt.Errorf("incomplete shipping quote"))
	}
	return &domain.ShippingQuote{VendorID: vendorID, FeeAmount: d.FeeAmount, Currency: d.Currency, CarrierID: d.CarrierID,
		ZoneID: d.ZoneID, FeeRuleID: d.FeeRuleID, FeeRuleVersion: d.FeeRuleVersion, PackageWeightGrams: d.PackageWeightGrams, QuotedAt: d.QuotedAt, ExpiresAt: d.ExpiresAt}, nil
}

// CreateShipment opens (or returns the existing) shipment of a paid vendor
// order. Shipment makes it idempotent per vendor order.
func (c *HTTPShipmentClient) CreateShipment(ctx context.Context, in CreateShipmentInput) (string, error) {
	var body struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/internal/shipments", in, &body); err != nil {
		return "", err
	}
	return body.Data.ID, nil
}

// CancelForVendorOrder voids (or asks the carrier to intercept) the
// shipment of a cancelled vendor order; a missing shipment is a no-op.
func (c *HTTPShipmentClient) CancelForVendorOrder(ctx context.Context, vendorOrderID string) error {
	return c.post(ctx, "/internal/shipments/by-vendor-order/"+url.PathEscape(vendorOrderID)+"/cancel", map[string]string{}, nil)
}
