// Package payos adapts the payOS payment-link and webhook contract without
// letting provider payloads leak into Payment's domain/usecase layers.
//
// Contract checked against https://payos.vn/docs (2026-09):
//   - POST /v2/payment-requests with an integer orderCode unique per
//     merchant; Payment passes its persisted reference, so a retry after a
//     timeout reuses it and payOS refuses a second link for it;
//   - description is at most 9 characters for bank accounts not linked
//     through payOS;
//   - amounts are integer VND;
//   - GET /v2/payment-requests/{id} and POST /v2/payment-requests/{id}/cancel;
//   - webhook signature: HMAC-SHA256 with the checksum key over the data
//     fields sorted by key as key=value joined by '&', null values as "".
//
// payOS has no refund API; refunds are manual (see the refund workflow).
package payos

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"shopee/backend/services/payment/internal/provider"
)

type Provider struct {
	clientID, apiKey, checksumKey, baseURL string
	client                                 *http.Client
}

func New(clientID, apiKey, checksumKey, baseURL string) *Provider {
	return &Provider{clientID: clientID, apiKey: apiKey, checksumKey: checksumKey, baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 15 * time.Second}}
}

// maxOrderCode is JavaScript's largest safe integer, payOS's orderCode bound.
const maxOrderCode = 9007199254740991

type createRequest struct {
	ExpiredAt   *int64 `json:"expiredAt,omitempty"`
	OrderCode   int64  `json:"orderCode"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	ReturnURL   string `json:"returnUrl"`
	CancelURL   string `json:"cancelUrl"`
	Signature   string `json:"signature"`
}

type envelope struct {
	Code string          `json:"code"`
	Desc string          `json:"desc"`
	Data json.RawMessage `json:"data"`
}

func orderCode(reference string) (int64, error) {
	code, err := strconv.ParseInt(reference, 10, 64)
	if err != nil || code <= 0 || code > maxOrderCode {
		return 0, fmt.Errorf("%w: payos reference must be a positive integer orderCode", provider.ErrRejected)
	}
	return code, nil
}

// Description stays within 9 characters and lets the buyer's bank statement
// be matched to the attempt: "DH" plus the last 7 digits of the orderCode.
func description(code int64) string {
	return fmt.Sprintf("DH%07d", code%10_000_000)
}

func (p *Provider) CreateIntent(ctx context.Context, in provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	if in.Currency != "VND" {
		return provider.CreateIntentResult{}, fmt.Errorf("%w: payos only supports VND", provider.ErrRejected)
	}
	code, err := orderCode(in.Reference)
	if err != nil {
		return provider.CreateIntentResult{}, err
	}
	reqBody := createRequest{OrderCode: code, Amount: in.Amount, Description: description(code), ReturnURL: in.ReturnURL, CancelURL: in.CancelURL}
	if in.ExpiresAt != nil {
		deadline := in.ExpiresAt.Unix()
		if deadline <= time.Now().Unix() || deadline > 2147483647 {
			return provider.CreateIntentResult{}, fmt.Errorf("%w: invalid payment deadline", provider.ErrRejected)
		}
		reqBody.ExpiredAt = &deadline
	}
	reqBody.Signature = p.signValues(map[string]string{
		"amount": strconv.FormatInt(reqBody.Amount, 10), "cancelUrl": reqBody.CancelURL, "description": reqBody.Description,
		"orderCode": strconv.FormatInt(reqBody.OrderCode, 10), "returnUrl": reqBody.ReturnURL,
	})
	var data struct {
		PaymentLinkID string `json:"paymentLinkId"`
		CheckoutURL   string `json:"checkoutUrl"`
		QRCode        string `json:"qrCode"`
	}
	if err := p.call(ctx, http.MethodPost, "/v2/payment-requests", reqBody, &data); err != nil {
		return provider.CreateIntentResult{}, fmt.Errorf("payos create link: %w", err)
	}
	if data.PaymentLinkID == "" || data.CheckoutURL == "" {
		return provider.CreateIntentResult{}, errors.New("payos create link: response without link")
	}
	return provider.CreateIntentResult{ProviderIntentID: data.PaymentLinkID, CheckoutURL: data.CheckoutURL, QRCode: data.QRCode, ExpiresAt: in.ExpiresAt}, nil
}

func (p *Provider) Query(ctx context.Context, reference string) (provider.LinkInfo, error) {
	if _, err := orderCode(reference); err != nil {
		return provider.LinkInfo{}, err
	}
	var data struct {
		ID           string `json:"id"`
		Amount       int64  `json:"amount"`
		AmountPaid   int64  `json:"amountPaid"`
		Status       string `json:"status"`
		Transactions []struct {
			Reference string `json:"reference"`
			Amount    int64  `json:"amount"`
		} `json:"transactions"`
	}
	if err := p.call(ctx, http.MethodGet, "/v2/payment-requests/"+url.PathEscape(reference), nil, &data); err != nil {
		return provider.LinkInfo{}, fmt.Errorf("payos query link: %w", err)
	}
	info := provider.LinkInfo{ProviderIntentID: data.ID, Amount: data.Amount, AmountPaid: data.AmountPaid, Currency: "VND", Status: linkStatus(data.Status, data.Amount, data.AmountPaid)}
	if len(data.Transactions) > 0 {
		info.TransactionReference = data.Transactions[0].Reference
	}
	return info, nil
}

// linkStatus maps payOS statuses. Anything not clearly paid or closed stays
// open, so a link is never treated as dead while it may still be paid.
func linkStatus(status string, amount, paid int64) provider.LinkStatus {
	switch strings.ToUpper(status) {
	case "PAID":
		if paid >= amount {
			return provider.LinkPaid
		}
		return provider.LinkOpen
	case "CANCELLED", "EXPIRED", "FAILED":
		return provider.LinkClosed
	default:
		return provider.LinkOpen
	}
}

func (p *Provider) Cancel(ctx context.Context, reference, reason string) error {
	if _, err := orderCode(reference); err != nil {
		return err
	}
	body := map[string]string{"cancellationReason": reason}
	if err := p.call(ctx, http.MethodPost, "/v2/payment-requests/"+url.PathEscape(reference)+"/cancel", body, nil); err != nil {
		return fmt.Errorf("payos cancel link: %w", err)
	}
	return nil
}

// call sends an authenticated request. HTTP 404 is ErrNotFound; a 4xx or a
// non-"00" payOS code is ErrRejected; anything else (transport error, 5xx)
// has an unknown outcome.
func (p *Provider) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", p.clientID)
	req.Header.Set("x-api-key", p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return provider.ErrNotFound
	case resp.StatusCode >= 500:
		return fmt.Errorf("status %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: status %d", provider.ErrRejected, resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if env.Code != "00" {
		// Only the provider's code is kept; its message may echo request data.
		return fmt.Errorf("%w: payos code %s", provider.ErrRejected, env.Code)
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("invalid response data: %w", err)
		}
	}
	return nil
}

// Verify checks the signed payload before mapping it to Payment's narrow event.
func (p *Provider) Verify(payload []byte, _ string) (provider.WebhookEvent, error) {
	var raw struct {
		Code      string          `json:"code"`
		Success   bool            `json:"success"`
		Signature string          `json:"signature"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: %w", err)
	}
	if raw.Signature == "" || len(raw.Data) == 0 {
		return provider.WebhookEvent{}, errors.New("payos webhook: missing signed data")
	}
	// payOS signs the complete data object. Decode numbers as written so a
	// large orderCode or a round amount is signed exactly as sent.
	decoder := json.NewDecoder(bytes.NewReader(raw.Data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: %w", err)
	}
	expected, err := p.signFields(fields)
	if err != nil {
		return provider.WebhookEvent{}, err
	}
	if !hmac.Equal([]byte(expected), []byte(raw.Signature)) {
		return provider.WebhookEvent{}, errors.New("payos webhook: invalid signature")
	}
	var data struct {
		OrderCode     int64  `json:"orderCode"`
		Amount        int64  `json:"amount"`
		Currency      string `json:"currency"`
		Reference     string `json:"reference"`
		PaymentLinkID string `json:"paymentLinkId"`
		Code          string `json:"code"`
	}
	if err := json.Unmarshal(raw.Data, &data); err != nil {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: %w", err)
	}
	if data.PaymentLinkID == "" || data.OrderCode <= 0 {
		return provider.WebhookEvent{}, errors.New("payos webhook: missing payment reference")
	}
	currency := data.Currency
	if currency == "" {
		currency = "VND"
	}
	reference := strconv.FormatInt(data.OrderCode, 10)
	// The bank transaction reference identifies the payment; without one the
	// link and result code do (a link settles at most once).
	eventID := "txn:" + data.Reference
	if data.Reference == "" {
		eventID = "link:" + data.PaymentLinkID + ":" + raw.Code + ":" + data.Code
	}
	event := provider.WebhookEvent{ProviderEventID: eventID, ProviderIntentID: data.PaymentLinkID, ProviderReference: reference, Amount: data.Amount, Currency: currency}
	if raw.Success && raw.Code == "00" && data.Code == "00" {
		event.Type = provider.EventPaymentSucceeded
	} else {
		event.Type = provider.EventPaymentFailed
		event.FailureReason = "payos payment was not successful"
	}
	return event, nil
}

// signFields renders payOS's canonical form of a data object.
func (p *Provider) signFields(fields map[string]any) (string, error) {
	values := make(map[string]string, len(fields))
	for k, v := range fields {
		switch val := v.(type) {
		case nil:
			values[k] = ""
		case string:
			if val == "null" || val == "undefined" {
				val = ""
			}
			values[k] = val
		case json.Number:
			values[k] = val.String()
		case bool:
			values[k] = strconv.FormatBool(val)
		default:
			// Nested objects and arrays: JSON with keys sorted (encoding/json
			// sorts map keys).
			encoded, err := json.Marshal(val)
			if err != nil {
				return "", fmt.Errorf("payos webhook: %w", err)
			}
			values[k] = string(encoded)
		}
	}
	return p.signValues(values), nil
}

func (p *Provider) signValues(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	mac := hmac.New(sha256.New, []byte(p.checksumKey))
	mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}
