package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// AddressCheckClient asks Shipment, which owns the carrier integration, to
// have the carrier check an address (PW-042):
// POST <shipment>/internal/shipments/address-checks.
type AddressCheckClient struct {
	URL    string
	Key    string
	client *http.Client
}

func NewAddressCheckClient(url, key string) *AddressCheckClient {
	return &AddressCheckClient{URL: url, Key: key, client: telemetry.NewHTTPClient(15 * time.Second)}
}

// CheckAddress returns usecase.ErrAddressCheckUnsupported when the carrier
// has no address check; any other error is transient.
func (c *AddressCheckClient) CheckAddress(ctx context.Context, a domain.VendorAddress) (bool, string, string, error) {
	body, err := json.Marshal(map[string]string{"recipient_name": a.RecipientName, "phone": a.Phone, "province": a.Province,
		"district": a.District, "ward": a.Ward, "street_address": a.StreetAddress})
	if err != nil {
		return false, "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/internal/shipments/address-checks", bytes.NewReader(body))
	if err != nil {
		return false, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := c.client.Do(req)
	if err != nil {
		return false, "", "", fmt.Errorf("shipment address check: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotImplemented:
		return false, "", "", usecase.ErrAddressCheckUnsupported
	default:
		return false, "", "", fmt.Errorf("shipment address check answered %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Deliverable bool   `json:"deliverable"`
			Reason      string `json:"reason"`
			Reference   string `json:"reference"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return false, "", "", fmt.Errorf("shipment address check: unreadable answer: %w", err)
	}
	return out.Data.Deliverable, out.Data.Reason, out.Data.Reference, nil
}
