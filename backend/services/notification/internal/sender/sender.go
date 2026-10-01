// Package sender defines Notification's boundary with an outbound email
// provider. The use case depends only on this interface, never on a
// concrete provider SDK.
package sender

import (
	"context"
	"errors"
)

type Email struct {
	ToEmail string
	Subject string
	Body    string
	// Type and Reference are for logs only.
	Type      string
	Reference string
}

// Sender delivers one email. Implementations never log the body or the
// address, and return a *Failure saying whether retrying can help.
type Sender interface {
	Send(ctx context.Context, email Email) error
}

// Failure is a delivery error with a short reason safe to store and show
// (no address, body or credential).
type Failure struct {
	Reason string
	// Permanent: the provider refused this message or recipient; sending
	// it again cannot succeed.
	Permanent bool
	// Uncertain: the failure came after the message was handed over, so it
	// may have been delivered; a retry can duplicate it (at-least-once).
	Uncertain bool
}

func (f *Failure) Error() string { return f.Reason }

func Transient(reason string) *Failure { return &Failure{Reason: reason} }
func Permanent(reason string) *Failure { return &Failure{Reason: reason, Permanent: true} }
func Uncertain(reason string) *Failure { return &Failure{Reason: reason, Uncertain: true} }

// Classify reads a send error: anything that is not a *Failure is treated
// as transient with a generic reason (its text may carry detail).
func Classify(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return Transient("delivery failed")
}
