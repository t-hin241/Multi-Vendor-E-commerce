// Package mock is a local stand-in for a real payment processor (Stripe,
// PayPal, ...), used until this deployment has real provider credentials.
// It implements the same provider.Provider/provider.Verifier interfaces a
// real adapter would, so swapping one in later is adding a new package, not
// touching usecase or domain code. It signs every event with an HMAC over a
// configured shared secret so the webhook handler's signature-verification
// code path is exercised even without a real provider.
package mock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"context"

	"github.com/google/uuid"

	"shopee/backend/services/payment/internal/provider"
)

type Provider struct {
	webhookSecret string
}

func New(webhookSecret string) *Provider {
	return &Provider{webhookSecret: webhookSecret}
}

func (p *Provider) CreateIntent(_ context.Context, _ provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	return provider.CreateIntentResult{ProviderIntentID: "mock_pi_" + uuid.NewString()}, nil
}

type eventPayload struct {
	ProviderEventID  string `json:"provider_event_id"`
	ProviderIntentID string `json:"provider_intent_id"`
	Type             string `json:"type"`
	Amount           int64  `json:"amount"`
	Currency         string `json:"currency"`
	FailureReason    string `json:"failure_reason,omitempty"`
}

// BuildSignedEvent stands in for a real provider's hosted checkout page
// telling us how a payment went. It's used by the "simulate outcome"
// endpoint, which feeds the result through the exact same signature
// verification and webhook processing path a real delivery would go
// through, so the two code paths can never drift apart.
func (p *Provider) BuildSignedEvent(providerIntentID string, amount int64, currency string, succeeded bool, failureReason string) (payload []byte, signature string, err error) {
	evt := eventPayload{
		ProviderEventID:  "mock_evt_" + uuid.NewString(),
		ProviderIntentID: providerIntentID,
		Amount:           amount,
		Currency:         currency,
	}
	if succeeded {
		evt.Type = string(provider.EventPaymentSucceeded)
	} else {
		evt.Type = string(provider.EventPaymentFailed)
		evt.FailureReason = failureReason
	}

	payload, err = json.Marshal(evt)
	if err != nil {
		return nil, "", err
	}
	return payload, p.sign(payload), nil
}

func (p *Provider) sign(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

var ErrInvalidSignature = errors.New("mock provider: invalid webhook signature")

// Verify checks the HMAC signature over payload before trusting any of it —
// the same discipline a real provider's signature scheme (e.g. Stripe's
// Stripe-Signature header) enforces.
func (p *Provider) Verify(payload []byte, signatureHeader string) (provider.WebhookEvent, error) {
	expected := p.sign(payload)
	if !hmac.Equal([]byte(expected), []byte(signatureHeader)) {
		return provider.WebhookEvent{}, ErrInvalidSignature
	}

	var evt eventPayload
	if err := json.Unmarshal(payload, &evt); err != nil {
		return provider.WebhookEvent{}, fmt.Errorf("mock provider: invalid payload: %w", err)
	}

	return provider.WebhookEvent{
		ProviderEventID:  evt.ProviderEventID,
		ProviderIntentID: evt.ProviderIntentID,
		Type:             provider.WebhookEventType(evt.Type),
		Amount:           evt.Amount,
		Currency:         evt.Currency,
		FailureReason:    evt.FailureReason,
	}, nil
}
