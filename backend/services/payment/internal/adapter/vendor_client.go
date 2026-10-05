package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// ErrNoPayoutDestination: the vendor has no verified default payout account.
var ErrNoPayoutDestination = errors.New("vendor has no verified payout destination")

// PayoutDestination is a vendor's verified default payout account, masked:
// Payment never receives the full account number.
type PayoutDestination struct {
	AccountID string `json:"account_id"`
	Version   int64  `json:"version"`
	BankBIN   string `json:"bank_bin"`
	Last4     string `json:"last4"`
}

type HTTPVendorClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPVendorClient(baseURL, key string) *HTTPVendorClient {
	return &HTTPVendorClient{baseURL: strings.TrimRight(baseURL, "/"), key: key, client: telemetry.NewHTTPClient(5 * time.Second)}
}

func (c *HTTPVendorClient) DefaultDestination(ctx context.Context, vendorID string) (*PayoutDestination, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/payout-destinations/"+url.PathEscape(vendorID)+"/default", nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(fmt.Errorf("vendor service unreachable: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoPayoutDestination
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apperror.Internal(fmt.Errorf("vendor service returned status %d for payout destination", resp.StatusCode))
	}
	var envelope struct {
		Data PayoutDestination `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&envelope); err != nil {
		return nil, apperror.Internal(err)
	}
	d := envelope.Data
	if d.AccountID == "" || d.Version < 1 || len(d.BankBIN) != 6 || len(d.Last4) != 4 {
		return nil, apperror.Internal(errors.New("invalid payout destination from vendor service"))
	}
	return &d, nil
}
