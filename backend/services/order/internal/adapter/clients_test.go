package adapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/order/internal/domain"
)

const testKey = "fake-test-service-key-not-a-real-secret"

func TestShipmentQuoteMapsUnavailableAndRequiresCompleteQuotes(t *testing.T) {
	status, body := 200, `{"data":{"vendor_id":"v","fee_amount":22000,"currency":"VND","carrier_id":"c","zone_id":"z","fee_rule_id":"r","fee_rule_version":4,"package_weight_grams":900,"quoted_at":"2026-09-30T00:00:00Z"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != testKey || r.URL.Path != "/internal/shipments/quotes" {
			t.Errorf("unexpected request %s %v", r.URL.Path, r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := NewHTTPShipmentClient(server.URL, testKey)

	q, err := client.Quote(t.Context(), "v", "HN", 900)
	if err != nil || q.FeeAmount != 22000 || q.FeeRuleVersion != 4 || q.Currency != "VND" {
		t.Fatalf("unexpected quote %+v %v", q, err)
	}
	status, body = 400, `{"error":{"code":"validation_error","message":"Shipping is not available for this destination yet"}}`
	_, err = client.Quote(t.Context(), "v", "XX", 900)
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != domain.CodeShippingUnavailable {
		t.Fatalf("expected shipping_unavailable, got %v", err)
	}
	status, body = 200, `{"data":{"fee_amount":0}}`
	if _, err := client.Quote(t.Context(), "v", "HN", 900); err == nil {
		t.Fatal("an incomplete quote must never be treated as free shipping")
	}
}

func TestPaymentRefundRequestKeepsPaymentsRefusal(t *testing.T) {
	status, body := 201, `{"data":{"payment_refund_id":"pr-1","status":"awaiting_provider_refund"}}`
	var got RefundRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != testKey {
			t.Error("missing service key")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := NewHTTPPaymentClient(server.URL, testKey)

	receipt, err := client.RequestRefund(t.Context(), RefundRequest{RefundID: "r1", OrderID: "o1", Amount: 100, Currency: "VND", Reason: "x", RequestedBy: "a"})
	if err != nil || receipt.PaymentRefundID != "pr-1" || got.RefundID != "r1" {
		t.Fatalf("unexpected %+v %v %+v", receipt, err, got)
	}
	status, body = 409, `{"error":{"code":"conflict","message":"Refund exceeds the captured amount"}}`
	_, err = client.RequestRefund(t.Context(), RefundRequest{RefundID: "r2"})
	var app *apperror.Error
	if !errors.As(err, &app) || app.Status != 409 || app.Message != "Refund exceeds the captured amount" {
		t.Fatalf("expected Payment's refusal, got %v", err)
	}
	status = 503
	_, err = client.RequestRefund(t.Context(), RefundRequest{RefundID: "r3"})
	if !errors.As(err, &app) || app.Code != apperror.CodeInternal {
		t.Fatalf("an outage must be retryable, got %v", err)
	}
}

// The notify call carries the service key and the effect id; a refusal is
// a 4xx the effect runner treats as permanent, an outage is retried.
func TestNotificationClientIdentifiesTheEventAndMapsAnswers(t *testing.T) {
	status := http.StatusAccepted
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != testKey {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer server.Close()
	client := NewHTTPNotificationClient(server.URL, testKey)
	if err := client.Notify(t.Context(), "effect-1", "user-1", "order_paid", "order-1"); err != nil {
		t.Fatal(err)
	}
	if got["event_id"] != "effect-1" || got["source"] != "order" || got["reference_id"] != "order-1" {
		t.Fatalf("unexpected payload %v", got)
	}
	var app *apperror.Error
	status = http.StatusBadRequest
	if err := client.Notify(t.Context(), "effect-1", "user-1", "order_paid", "order-1"); !errors.As(err, &app) || app.Status != 400 {
		t.Fatalf("a refusal keeps its 4xx status, got %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := client.Notify(t.Context(), "effect-1", "user-1", "order_paid", "order-1"); !errors.As(err, &app) || app.Code != apperror.CodeInternal {
		t.Fatalf("an outage is retried, got %v", err)
	}
}
