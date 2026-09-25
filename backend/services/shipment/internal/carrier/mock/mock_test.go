package mock_test

import (
	"context"
	"testing"

	"shopee/backend/services/shipment/internal/carrier"
	"shopee/backend/services/shipment/internal/carrier/mock"
)

func TestBuildSignedEventAndVerify_RoundTrips(t *testing.T) {
	p := mock.New("test-secret")

	payload, signature, err := p.BuildSignedEvent("mock_intercept_123", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event, err := p.Verify(payload, signature)
	if err != nil {
		t.Fatalf("unexpected error verifying a correctly signed payload: %v", err)
	}
	if event.ProviderReferenceID != "mock_intercept_123" {
		t.Errorf("expected provider_reference_id mock_intercept_123, got %q", event.ProviderReferenceID)
	}
	if !event.Accepted {
		t.Errorf("expected accepted=true to round-trip")
	}
}

func TestVerify_RejectsWrongSignature(t *testing.T) {
	p := mock.New("test-secret")

	payload, _, err := p.BuildSignedEvent("mock_intercept_123", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := p.Verify(payload, "not-the-real-signature"); err == nil {
		t.Error("expected an error for a tampered/wrong signature")
	}
}

func TestVerify_RejectsSignatureFromADifferentSecret(t *testing.T) {
	a := mock.New("secret-a")
	b := mock.New("secret-b")

	payload, signature, err := a.BuildSignedEvent("mock_intercept_123", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := b.Verify(payload, signature); err == nil {
		t.Error("expected an error verifying a signature made with a different secret")
	}
}

func TestBuildSignedEvent_RejectedDecisionCarriesReason(t *testing.T) {
	p := mock.New("test-secret")

	payload, signature, err := p.BuildSignedEvent("mock_intercept_123", false, "already out for delivery")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event, err := p.Verify(payload, signature)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Accepted {
		t.Errorf("expected accepted=false to round-trip")
	}
	if event.Reason != "already out for delivery" {
		t.Errorf("expected reason to round-trip, got %q", event.Reason)
	}
}

func TestRequestInterception_ReturnsAReferenceIDWithNoDecisionYet(t *testing.T) {
	p := mock.New("test-secret")

	result, err := p.RequestInterception(context.Background(), carrier.RequestInterceptionInput{
		ShipmentID: "shipment-1", TrackingNumber: "TRACK123", CarrierID: "carrier-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProviderReferenceID == "" {
		t.Error("expected a non-empty provider reference id")
	}
}
