// Package provider defines Payment's boundary with an external payment
// processor. Domain and usecase code depend only on these interfaces, never
// on a concrete provider SDK.
package provider

import (
	"context"
	"errors"
	"time"
)

type CreateIntentInput struct {
	// Reference is Payment's stable reference for this attempt, persisted
	// before the call. The provider must key the link on it so a retried
	// or timed-out call can be resolved by Query instead of charging twice.
	Reference string
	ExpiresAt *time.Time
	OrderID   string
	Amount    int64
	Currency  string
	ReturnURL string
	CancelURL string
}

type CreateIntentResult struct {
	ProviderIntentID string
	CheckoutURL      string
	QRCode           string
	ExpiresAt        *time.Time
}

// LinkStatus is the provider's view of a payment link.
type LinkStatus string

const (
	// LinkOpen: can still be paid.
	LinkOpen LinkStatus = "open"
	// LinkPaid: the full amount was paid.
	LinkPaid LinkStatus = "paid"
	// LinkClosed: expired, cancelled or failed without payment.
	LinkClosed LinkStatus = "closed"
)

// LinkInfo is what Query returns for a reference.
type LinkInfo struct {
	ProviderIntentID string
	Status           LinkStatus
	Amount           int64
	AmountPaid       int64
	Currency         string
	// TransactionReference identifies the payment that settled the link
	// (the bank reference for payOS), used as the receipt's event ID.
	TransactionReference string
}

var (
	// ErrNotFound: the provider has no link for the reference.
	ErrNotFound = errors.New("provider: payment link not found")
	// ErrRejected: the provider definitively refused the request (it will
	// not succeed on retry). Any other error may be a timeout whose outcome
	// is unknown and must be resolved by Query.
	ErrRejected = errors.New("provider: request rejected")
)

// Provider is a payment processor's payment-link API.
type Provider interface {
	CreateIntent(ctx context.Context, in CreateIntentInput) (CreateIntentResult, error)
	// Query reports the link created for reference, or ErrNotFound.
	Query(ctx context.Context, reference string) (LinkInfo, error)
	// Cancel closes an unpaid link so it can no longer be paid.
	Cancel(ctx context.Context, reference, reason string) error
}

type WebhookEventType string

const (
	EventPaymentSucceeded WebhookEventType = "payment.succeeded"
	EventPaymentFailed    WebhookEventType = "payment.failed"
)

// WebhookEvent is a provider's webhook delivery, already verified and
// parsed. Amount/Currency are what the provider says it collected; the use
// case checks them against its own record before trusting them.
type WebhookEvent struct {
	// ProviderEventID is stable for one payment: derived from the verified
	// transaction reference, never from receive time or the signature.
	ProviderEventID   string
	ProviderIntentID  string
	ProviderReference string
	Type              WebhookEventType
	Amount            int64
	Currency          string
	FailureReason     string
}

// Verifier authenticates and parses a raw webhook delivery. Implementations
// must check the provider's signature before returning an event.
type Verifier interface {
	Verify(payload []byte, signatureHeader string) (WebhookEvent, error)
}
