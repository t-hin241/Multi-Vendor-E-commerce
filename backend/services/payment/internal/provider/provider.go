// Package provider defines Payment's boundary with an external payment
// processor. Domain and usecase code depend only on these interfaces, never
// on a concrete Stripe/PayPal SDK — swapping the mock adapter used today for
// a real one later means adding a new implementation of this package, not
// touching business logic.
package provider

import "context"

type CreateIntentInput struct {
	OrderID  string
	Amount   int64
	Currency string
}

type CreateIntentResult struct {
	ProviderIntentID string
}

// Provider creates a payment intent with an external payment processor.
type Provider interface {
	CreateIntent(ctx context.Context, in CreateIntentInput) (CreateIntentResult, error)
}

type WebhookEventType string

const (
	EventPaymentSucceeded WebhookEventType = "payment.succeeded"
	EventPaymentFailed    WebhookEventType = "payment.failed"
)

// WebhookEvent is a provider's webhook delivery, already verified and
// parsed. Amount/Currency are what the provider says it collected — the use
// case checks them against its own record of what was requested before
// ever trusting that a payment succeeded.
type WebhookEvent struct {
	ProviderEventID  string
	ProviderIntentID string
	Type             WebhookEventType
	Amount           int64
	Currency         string
	FailureReason    string
}

// Verifier authenticates and parses a raw webhook delivery. Implementations
// must check the provider's signature scheme before returning a WebhookEvent
// a caller can act on.
type Verifier interface {
	Verify(payload []byte, signatureHeader string) (WebhookEvent, error)
}
