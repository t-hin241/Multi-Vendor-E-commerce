// Package payos adapts the payOS payment-link and webhook contract without
// letting provider payloads leak into Payment's domain/usecase layers.
package payos

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"shopee/backend/services/payment/internal/provider"
)

type Provider struct {
	clientID, apiKey, checksumKey, baseURL string
	client                                 *http.Client
	nextOrderCode                          atomic.Int64
}

func New(clientID, apiKey, checksumKey, baseURL string) *Provider {
	p := &Provider{clientID: clientID, apiKey: apiKey, checksumKey: checksumKey, baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 15 * time.Second}}
	p.nextOrderCode.Store(time.Now().UnixNano() / 1_000_000)
	return p
}

type createRequest struct {
	ExpiredAt   *int64 `json:"expiredAt,omitempty"`
	OrderCode   int64  `json:"orderCode"`
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	ReturnURL   string `json:"returnUrl"`
	CancelURL   string `json:"cancelUrl"`
	Signature   string `json:"signature"`
}
type apiResponse struct {
	Code string `json:"code"`
	Desc string `json:"desc"`
	Data struct {
		PaymentLinkID string `json:"paymentLinkId"`
		CheckoutURL   string `json:"checkoutUrl"`
		QRCode        string `json:"qrCode"`
	} `json:"data"`
}

func (p *Provider) CreateIntent(ctx context.Context, in provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	if in.Currency != "VND" {
		return provider.CreateIntentResult{}, fmt.Errorf("payos: only VND is supported")
	}
	code := p.nextOrderCode.Add(1)
	reqBody := createRequest{OrderCode: code, Amount: in.Amount, Description: "Thanh toan don hang " + shortOrder(in.OrderID), ReturnURL: in.ReturnURL, CancelURL: in.CancelURL}
	if in.ExpiresAt != nil {
		deadline := in.ExpiresAt.Unix()
		if deadline <= time.Now().Unix() || deadline > 2147483647 {
			return provider.CreateIntentResult{}, fmt.Errorf("payos: invalid payment deadline")
		}
		reqBody.ExpiredAt = &deadline
	}
	reqBody.Signature = p.sign(map[string]any{"amount": reqBody.Amount, "cancelUrl": reqBody.CancelURL, "description": reqBody.Description, "orderCode": reqBody.OrderCode, "returnUrl": reqBody.ReturnURL})
	body, err := json.Marshal(reqBody)
	if err != nil {
		return provider.CreateIntentResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v2/payment-requests", strings.NewReader(string(body)))
	if err != nil {
		return provider.CreateIntentResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", p.clientID)
	req.Header.Set("x-api-key", p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return provider.CreateIntentResult{}, fmt.Errorf("payos create link: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return provider.CreateIntentResult{}, fmt.Errorf("payos create link: status %d", resp.StatusCode)
	}
	var decoded apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return provider.CreateIntentResult{}, err
	}
	if decoded.Code != "00" || decoded.Data.PaymentLinkID == "" || decoded.Data.CheckoutURL == "" {
		return provider.CreateIntentResult{}, fmt.Errorf("payos create link: %s", decoded.Desc)
	}
	return provider.CreateIntentResult{ProviderIntentID: decoded.Data.PaymentLinkID, CheckoutURL: decoded.Data.CheckoutURL, QRCode: decoded.Data.QRCode, ExpiresAt: in.ExpiresAt}, nil
}

func shortOrder(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
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
	if data.PaymentLinkID == "" || raw.Signature == "" {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: missing signed payment reference")
	}
	// payOS signs the complete data object. Preserve all returned fields rather
	// than signing a hand-maintained subset that could silently drift when the
	// provider adds a field.
	var fields map[string]any
	if err := json.Unmarshal(raw.Data, &fields); err != nil {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: %w", err)
	}
	if !hmac.Equal([]byte(p.sign(fields)), []byte(raw.Signature)) {
		return provider.WebhookEvent{}, fmt.Errorf("payos webhook: invalid signature")
	}
	eventID := data.Reference
	if eventID == "" {
		eventID = data.PaymentLinkID + ":" + strconv.FormatInt(data.OrderCode, 10)
	}
	event := provider.WebhookEvent{ProviderEventID: eventID, ProviderIntentID: data.PaymentLinkID, Amount: data.Amount, Currency: data.Currency}
	if raw.Success && raw.Code == "00" && data.Code == "00" {
		event.Type = provider.EventPaymentSucceeded
	} else {
		event.Type = provider.EventPaymentFailed
		event.FailureReason = "payos payment was not successful"
	}
	return event, nil
}

func (p *Provider) sign(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fmt.Sprint(values[k]))
	}
	mac := hmac.New(sha256.New, []byte(p.checksumKey))
	mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}
