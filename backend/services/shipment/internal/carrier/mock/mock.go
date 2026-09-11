// Package mock is a local stand-in for a real carrier's interception API,
// used until this deployment integrates a real one. It implements the same
// carrier.Provider/carrier.Verifier interfaces a real adapter would, so
// swapping one in later is adding a new package, not touching usecase or
// domain code. It signs every decision with an HMAC over a configured
// shared secret so the webhook handler's signature-verification code path
// is exercised even without a real carrier. Structurally identical to
// payment/internal/provider/mock.
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

	"shopee/backend/services/shipment/internal/carrier"
)

type Provider struct {
	webhookSecret string
}

func New(webhookSecret string) *Provider {
	return &Provider{webhookSecret: webhookSecret}
}

// RequestInterception returns a reference id immediately but no decision —
// a real carrier's dispatcher has to be reached first, so the outcome is
// always asynchronous, delivered later as a signed event.
func (p *Provider) RequestInterception(_ context.Context, _ carrier.RequestInterceptionInput) (carrier.RequestInterceptionResult, error) {
	return carrier.RequestInterceptionResult{ProviderReferenceID: "mock_intercept_" + uuid.NewString()}, nil
}

type eventPayload struct {
	ProviderReferenceID string `json:"provider_reference_id"`
	Accepted            bool   `json:"accepted"`
	Reason              string `json:"reason,omitempty"`
}

// BuildSignedEvent stands in for a real carrier's dispatcher calling back
// with a decision. It's used by the vendor-facing "simulate carrier
// decision" endpoint, which feeds the result through the exact same
// signature verification and webhook processing path a real delivery would
// go through, so the two code paths can never drift apart.
func (p *Provider) BuildSignedEvent(providerReferenceID string, accepted bool, reason string) (payload []byte, signature string, err error) {
	evt := eventPayload{ProviderReferenceID: providerReferenceID, Accepted: accepted, Reason: reason}

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

var ErrInvalidSignature = errors.New("mock carrier: invalid webhook signature")

// Verify checks the HMAC signature over payload before trusting any of it —
// the same discipline a real carrier's signature scheme would enforce.
func (p *Provider) Verify(payload []byte, signatureHeader string) (carrier.DecisionEvent, error) {
	expected := p.sign(payload)
	if !hmac.Equal([]byte(expected), []byte(signatureHeader)) {
		return carrier.DecisionEvent{}, ErrInvalidSignature
	}

	var evt eventPayload
	if err := json.Unmarshal(payload, &evt); err != nil {
		return carrier.DecisionEvent{}, fmt.Errorf("mock carrier: invalid payload: %w", err)
	}

	return carrier.DecisionEvent{
		ProviderReferenceID: evt.ProviderReferenceID,
		Accepted:            evt.Accepted,
		Reason:              evt.Reason,
	}, nil
}
