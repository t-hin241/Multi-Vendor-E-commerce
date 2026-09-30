// Package mock is a local stand-in for a real payment processor, used until
// a deployment has real provider credentials. It implements the same
// provider interfaces a real adapter does and signs every event with an
// HMAC over a configured shared secret, so the webhook verification path is
// exercised without a real provider. Link state is kept in memory only.
package mock

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"shopee/backend/services/payment/internal/provider"
)

const intentPrefix = "mock_pi_"

type Provider struct {
	webhookSecret string
	mu            sync.Mutex
	links         map[string]provider.LinkInfo
}

func New(webhookSecret string) *Provider {
	return &Provider{webhookSecret: webhookSecret, links: map[string]provider.LinkInfo{}}
}

// CreateIntent is idempotent per reference, like a real provider keyed on it.
func (p *Provider) CreateIntent(_ context.Context, in provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	if in.Reference == "" {
		return provider.CreateIntentResult{}, fmt.Errorf("%w: missing reference", provider.ErrRejected)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	link, ok := p.links[in.Reference]
	if !ok {
		link = provider.LinkInfo{ProviderIntentID: intentPrefix + in.Reference, Status: provider.LinkOpen, Amount: in.Amount, Currency: in.Currency}
		p.links[in.Reference] = link
	}
	return provider.CreateIntentResult{ProviderIntentID: link.ProviderIntentID, ExpiresAt: in.ExpiresAt}, nil
}

func (p *Provider) Query(_ context.Context, reference string) (provider.LinkInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	link, ok := p.links[reference]
	if !ok {
		return provider.LinkInfo{}, provider.ErrNotFound
	}
	return link, nil
}

func (p *Provider) Cancel(_ context.Context, reference, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	link, ok := p.links[reference]
	if !ok {
		return provider.ErrNotFound
	}
	if link.Status == provider.LinkOpen {
		link.Status = provider.LinkClosed
		p.links[reference] = link
	}
	return nil
}

type eventPayload struct {
	ProviderEventID   string `json:"provider_event_id"`
	ProviderIntentID  string `json:"provider_intent_id"`
	ProviderReference string `json:"provider_reference"`
	Type              string `json:"type"`
	Amount            int64  `json:"amount"`
	Currency          string `json:"currency"`
	FailureReason     string `json:"failure_reason,omitempty"`
}

// BuildSignedEvent stands in for a real provider's hosted checkout page
// reporting a payment outcome. Its result goes through the same signature
// verification and receipt processing as a real delivery.
func (p *Provider) BuildSignedEvent(providerIntentID string, amount int64, currency string, succeeded bool, failureReason string) (payload []byte, signature string, err error) {
	evt := eventPayload{
		ProviderEventID:   "mock_evt_" + uuid.NewString(),
		ProviderIntentID:  providerIntentID,
		ProviderReference: strings.TrimPrefix(providerIntentID, intentPrefix),
		Amount:            amount,
		Currency:          currency,
	}
	if succeeded {
		evt.Type = string(provider.EventPaymentSucceeded)
		p.mu.Lock()
		if link, ok := p.links[evt.ProviderReference]; ok {
			link.Status, link.AmountPaid = provider.LinkPaid, amount
			p.links[evt.ProviderReference] = link
		}
		p.mu.Unlock()
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

// Verify checks the HMAC signature over payload before trusting any of it.
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
		ProviderEventID:   evt.ProviderEventID,
		ProviderIntentID:  evt.ProviderIntentID,
		ProviderReference: evt.ProviderReference,
		Type:              provider.WebhookEventType(evt.Type),
		Amount:            evt.Amount,
		Currency:          evt.Currency,
		FailureReason:     evt.FailureReason,
	}, nil
}
