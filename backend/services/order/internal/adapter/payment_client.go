package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

// RefundRequest asks Payment to return money for an order refund. RefundID
// is Order's refund id and Payment's idempotency key.
type RefundRequest struct {
	RefundID  string  `json:"order_refund_id"`
	OrderID   string  `json:"order_id"`
	PaymentID *string `json:"payment_id,omitempty"`
	// VendorOrderID names the package the refund charges in settlement.
	VendorOrderID *string `json:"vendor_order_id,omitempty"`
	Amount        int64   `json:"amount"`
	Currency      string  `json:"currency"`
	Reason        string  `json:"reason"`
	RequestedBy   string  `json:"requested_by"`
}

// RefundReceipt is Payment's answer: it accepted the refund and tracks it
// under PaymentRefundID. Money is not returned yet.
type RefundReceipt struct {
	PaymentRefundID string `json:"payment_refund_id"`
	Status          string `json:"status"`
}

// SettlementReport is a completed vendor order as Order snapshotted it.
type SettlementReport struct {
	VendorOrderID         string    `json:"vendor_order_id"`
	OrderID               string    `json:"order_id"`
	VendorID              string    `json:"vendor_id"`
	Currency              string    `json:"currency"`
	SubtotalAmount        int64     `json:"subtotal_amount"`
	ShippingAmount        int64     `json:"shipping_amount"`
	CommissionAmount      int64     `json:"commission_amount"`
	CommissionRateBps     int       `json:"commission_rate_bps"`
	CommissionRuleVersion *int64    `json:"commission_rule_version,omitempty"`
	CompletedAt           time.Time `json:"completed_at"`
	EligibleAt            time.Time `json:"eligible_at"`
}

type HTTPPaymentClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func NewHTTPPaymentClient(baseURL, key string) *HTTPPaymentClient {
	return &HTTPPaymentClient{baseURL: baseURL, key: key, client: &http.Client{Timeout: 5 * time.Second}}
}

// RequestRefund submits a refund to Payment. A 4xx answer is Payment
// refusing it (for example over the captured amount) and is returned with
// Payment's code and message; transport errors and 5xx are internal and
// retried by the caller.
func (c *HTTPPaymentClient) RequestRefund(ctx context.Context, r RefundRequest) (*RefundReceipt, error) {
	data, err := c.post(ctx, "/internal/payments/refunds", r, "Payment refused the refund request")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data RefundReceipt `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Data.PaymentRefundID == "" {
		return nil, apperror.Internal(fmt.Errorf("invalid refund receipt"))
	}
	return &envelope.Data, nil
}

// SettleVendorOrder reports a completed vendor order to Payment's
// settlement ledger. Payment records it once; a 4xx is Payment refusing it.
func (c *HTTPPaymentClient) SettleVendorOrder(ctx context.Context, r SettlementReport) error {
	_, err := c.post(ctx, "/internal/settlements/vendor-orders", r, "Payment refused the settlement report")
	return err
}

func (c *HTTPPaymentClient) post(ctx context.Context, path string, payload any, refusal string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(fmt.Errorf("payment service unreachable: %w", err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		var envelope errorEnvelope
		_ = json.Unmarshal(data, &envelope)
		msg := envelope.Error.Message
		if msg == "" {
			msg = refusal
		}
		code := apperror.Code(envelope.Error.Code)
		if code == "" {
			code = apperror.CodeConflict
		}
		return nil, &apperror.Error{Code: code, Message: msg, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, apperror.Internal(fmt.Errorf("payment service returned status %d", resp.StatusCode))
	}
	return data, nil
}
