package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/order/internal/domain"
)

// ReturnDestination reads the shop's verified return destination (AF-05);
// nil when the shop has none verified (404).
func (c *HTTPVendorClient) ReturnDestination(ctx context.Context, vendorID string) (*domain.ReturnDestination, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/return-destinations/"+url.PathEscape(vendorID), nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(fmt.Errorf("vendor service unreachable: %w", err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, apperror.Internal(fmt.Errorf("vendor service returned status %d for a return destination", resp.StatusCode))
	}
	var out struct {
		Data struct {
			AddressID      string `json:"address_id"`
			RecipientName  string `json:"recipient_name"`
			Phone          string `json:"phone"`
			Province       string `json:"province"`
			District       string `json:"district"`
			Ward           string `json:"ward"`
			StreetAddress  string `json:"street_address"`
			ReceivingHours string `json:"receiving_hours"`
			Version        int64  `json:"version"`
			Verified       bool   `json:"verified"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, apperror.Internal(err)
	}
	d := out.Data
	if !d.Verified {
		return nil, nil
	}
	return &domain.ReturnDestination{RecipientName: d.RecipientName, Phone: d.Phone, Province: d.Province, District: d.District, Ward: d.Ward,
		StreetAddress: d.StreetAddress, ReceivingHours: d.ReceivingHours, VendorAddressID: d.AddressID, DestinationVersion: d.Version}, nil
}

// ReturnShipmentAuthorization opens (or re-points before dispatch) the
// return's parcel at Shipment.
type ReturnShipmentAuthorization struct {
	OperationID          string             `json:"operation_id"`
	ReturnID             string             `json:"return_id"`
	OrderID              string             `json:"order_id"`
	VendorID             string             `json:"vendor_id"`
	BuyerID              string             `json:"buyer_id"`
	AuthorizationVersion int                `json:"authorization_version"`
	Destination          domain.Destination `json:"destination_snapshot"`
	ReceivingHours       string             `json:"receiving_hours"`
}

type returnShipmentResult struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// AuthorizeReturnShipment: Shipment answers the parcel id (the same for a
// repeat); 409 destination_changed / disabled is a refusal.
func (c *HTTPShipmentClient) AuthorizeReturnShipment(ctx context.Context, a ReturnShipmentAuthorization) (string, error) {
	var out returnShipmentResult
	if err := c.post(ctx, "/internal/shipments/return-shipments", a, &out); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", apperror.Internal(fmt.Errorf("shipment service answered no return shipment id"))
	}
	return out.Data.ID, nil
}

// DispatchReturnShipment forwards the buyer's carrier and tracking.
func (c *HTTPShipmentClient) DispatchReturnShipment(ctx context.Context, shipmentID, operationID, carrier, tracking string, at time.Time) error {
	return c.post(ctx, "/internal/shipments/return-shipments/"+url.PathEscape(shipmentID)+"/dispatches", map[string]any{
		"operation_id": operationID, "carrier_name": carrier, "tracking_number": tracking, "dispatched_at": at.UTC()}, nil)
}

// ReceiveReturnShipment closes the parcel once the shop recorded the goods.
func (c *HTTPShipmentClient) ReceiveReturnShipment(ctx context.Context, shipmentID string) error {
	return c.post(ctx, "/internal/shipments/return-shipments/"+url.PathEscape(shipmentID)+"/receipts", map[string]string{}, nil)
}

// ReturnShipmentException records a parcel lost on the way back.
func (c *HTTPShipmentClient) ReturnShipmentException(ctx context.Context, shipmentID, reason string) error {
	return c.post(ctx, "/internal/shipments/return-shipments/"+url.PathEscape(shipmentID)+"/exceptions", map[string]string{"reason": reason}, nil)
}
