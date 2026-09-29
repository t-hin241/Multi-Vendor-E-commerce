package payos

import (
	"testing"

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
