package mock_test

import (
	"testing"

	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/provider/mock"
)

func TestBuildSignedEventAndVerify_RoundTrips(t *testing.T) {
	p := mock.New("test-secret")

	payload, signature, err := p.BuildSignedEvent("pi_123", 1000, "VND", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event, err := p.Verify(payload, signature)
	if err != nil {
		t.Fatalf("unexpected error verifying a correctly signed payload: %v", err)
	}
	if event.ProviderIntentID != "pi_123" {
		t.Errorf("expected provider_intent_id pi_123, got %q", event.ProviderIntentID)
	}
	if event.Type != provider.EventPaymentSucceeded {
		t.Errorf("expected succeeded event type, got %q", event.Type)
	}
}

func TestVerify_RejectsWrongSignature(t *testing.T) {
	p := mock.New("test-secret")

	payload, _, err := p.BuildSignedEvent("pi_123", 1000, "VND", true, "")
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

	payload, signature, err := a.BuildSignedEvent("pi_123", 1000, "VND", true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := b.Verify(payload, signature); err == nil {
		t.Error("expected an error verifying a signature made with a different secret")
	}
}

func TestBuildSignedEvent_FailedOutcomeCarriesReason(t *testing.T) {
	p := mock.New("test-secret")

	payload, signature, err := p.BuildSignedEvent("pi_123", 1000, "VND", false, "insufficient_funds")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event, err := p.Verify(payload, signature)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != provider.EventPaymentFailed {
		t.Errorf("expected failed event type, got %q", event.Type)
	}
	if event.FailureReason != "insufficient_funds" {
		t.Errorf("expected failure reason to round-trip, got %q", event.FailureReason)
	}
}
