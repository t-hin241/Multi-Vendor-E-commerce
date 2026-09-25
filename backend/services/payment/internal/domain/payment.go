// Package domain holds Payment's entities and business rules: the payment
// intent status state machine, and the invariant that only a signed,
// amount-matching webhook event can ever move a payment forward.
package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusAuthorized Status = "authorized"
	StatusCaptured   Status = "captured"
	StatusFailed     Status = "failed"
	StatusRefunded   Status = "refunded"
)

// validTransitions is the only place a payment intent's lifecycle is
// defined. captured/failed/refunded are the terminal-ish states a webhook
// or a refund action can produce; pending can go straight to captured for a
// provider that auto-captures, or through authorized first for one that
// doesn't.
var validTransitions = map[Status][]Status{
	StatusPending:    {StatusAuthorized, StatusCaptured, StatusFailed},
	StatusAuthorized: {StatusCaptured, StatusFailed, StatusRefunded},
	StatusCaptured:   {StatusRefunded},
	StatusFailed:     {},
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

// PaymentIntent tracks one attempt to collect payment for an order. Amount
// and currency are captured from Order at creation time and never
// recomputed — a later webhook is checked against this snapshot, not
// against whatever Order says right now.
type PaymentIntent struct {
	ID               string
	OrderID          string
	BuyerID          string
	Amount           int64
	Currency         string
	Status           Status
	Provider         string
	ProviderIntentID string
	FailureReason    *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func ValidateAmount(amount int64) error {
	if amount <= 0 {
		return apperror.Validation("Amount must be a positive number")
	}
	return nil
}
