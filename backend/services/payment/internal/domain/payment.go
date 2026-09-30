// Package domain holds Payment's entities and business rules: the payment
// intent status state machine, the rules for applying a verified provider
// receipt, refunds and the settlement ledger.
package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	// StatusCreating: persisted before the provider call; the provider link
	// is not confirmed yet.
	StatusCreating   Status = "creating"
	StatusPending    Status = "pending"
	StatusAuthorized Status = "authorized"
	StatusCaptured   Status = "captured"
	StatusFailed     Status = "failed"
	StatusRefunded   Status = "refunded"
	// StatusExpired: the provider link closed without a payment (expired,
	// cancelled, or never created).
	StatusExpired Status = "expired"
)

// validTransitions is the only place a payment intent's lifecycle is
// defined. A capture is money that arrived, so it is accepted even on an
// intent that expired or failed earlier (a late or out-of-order success);
// Order then decides whether it can still pay the order.
var validTransitions = map[Status][]Status{
	StatusCreating:   {StatusPending, StatusCaptured, StatusFailed, StatusExpired},
	StatusPending:    {StatusAuthorized, StatusCaptured, StatusFailed, StatusExpired},
	StatusAuthorized: {StatusCaptured, StatusFailed, StatusRefunded},
	StatusCaptured:   {StatusRefunded},
	StatusFailed:     {StatusCaptured},
	StatusExpired:    {StatusCaptured},
	StatusRefunded:   {},
}

func CanTransition(from, to Status) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Open intents may still be paid through their provider link.
func (s Status) Open() bool { return s == StatusCreating || s == StatusPending }

// PaymentIntent tracks one attempt to collect payment for an order. Amount
// and currency are captured from Order at creation time and never
// recomputed; a later webhook is checked against this snapshot.
type PaymentIntent struct {
	ID                string
	OrderID           string
	BuyerID           string
	Amount            int64
	Currency          string
	Status            Status
	Provider          string
	ProviderIntentID  string
	ProviderReference string
	CheckoutURL       string
	QRCode            string
	ExpiresAt         *time.Time
	FailureReason     *string
	CreateAttempts    int
	LastError         *string
	ClosedReason      *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func ValidateAmount(amount int64) error {
	if amount <= 0 {
		return apperror.Validation("Amount must be a positive number")
	}
	return nil
}

// Payable reports whether the buyer can still be sent to this intent's
// provider link at now.
func (i *PaymentIntent) Payable(now time.Time) bool {
	return i.Status == StatusPending && (i.ExpiresAt == nil || i.ExpiresAt.After(now))
}
