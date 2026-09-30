package payos

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shopee/backend/services/payment/internal/provider"
)

func TestVerifyAcceptsSignedSucceededWebhook(t *testing.T) {
	p := New("client", "key", "checksum", "https://example.test")
	fields := map[string]any{"amount": int64(50000), "code": "00", "currency": "VND", "orderCode": int64(123), "paymentLinkId": "link-1", "reference": "transfer-1"}
	payload := []byte(`{"code":"00","success":true,"signature":"` + p.sign(fields) + `","data":{"orderCode":123,"amount":50000,"currency":"VND","reference":"transfer-1","paymentLinkId":"link-1","code":"00"}}`)
	event, err := p.Verify(payload, "")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if event.Type != provider.EventPaymentSucceeded || event.ProviderEventID != "transfer-1" || event.ProviderIntentID != "link-1" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	p := New("client", "key", "checksum", "https://example.test")
	payload := []byte(`{"code":"00","success":true,"signature":"wrong","data":{"orderCode":123,"amount":50000,"currency":"VND","reference":"transfer-1","paymentLinkId":"link-1","code":"00"}}`)
	if _, err := p.Verify(payload, ""); err == nil {
		t.Fatal("Verify() accepted invalid signature")
	}
}

func TestCreateIntentPropagatesInventoryDeadline(t *testing.T) {
	deadline := time.Now().Add(10 * time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body createRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.ExpiredAt == nil || *body.ExpiredAt != deadline.Unix() {
			t.Error("missing provider expiry")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"00","data":{"paymentLinkId":"test-link","checkoutUrl":"https://example.test/pay"}}`))
	}))
	defer server.Close()
	p := New("fake-client", "fake-key", "fake-checksum", server.URL)
	result, err := p.CreateIntent(t.Context(), provider.CreateIntentInput{OrderID: "test-order", Amount: 100, Currency: "VND", ExpiresAt: &deadline})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpiresAt == nil || !result.ExpiresAt.Equal(deadline) {
		t.Fatal("provider receipt deadline mismatch")
	}
}
