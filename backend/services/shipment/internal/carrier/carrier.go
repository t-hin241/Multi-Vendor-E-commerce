// Package carrier defines Shipment's boundary with an external carrier for
// delivery interception. Domain and usecase code depend only on these
// interfaces, never on a concrete carrier SDK — swapping the mock adapter
// used today for a real one later means adding a new implementation of this
// package, not touching business logic. Mirrors
// payment/internal/provider's Provider/Verifier split.
package carrier

import (
	"context"
	"errors"
)

type RequestInterceptionInput struct {
	ShipmentID     string
	TrackingNumber string
	CarrierID      string
}

type RequestInterceptionResult struct {
	// ProviderReferenceID identifies this interception request with the
	// carrier; the decision arrives later (via DecisionEvent), it is never
	// returned synchronously — a real carrier can't answer instantly either.
	ProviderReferenceID string
}

// Provider asks an external carrier to intercept an in-transit shipment.
type Provider interface {
	RequestInterception(ctx context.Context, in RequestInterceptionInput) (RequestInterceptionResult, error)
}

// DecisionEvent is a carrier's answer to a previously requested
// interception, already verified and parsed.
type DecisionEvent struct {
	// EventID identifies the delivery; a repeated delivery is ignored.
	EventID             string
	ProviderReferenceID string
	Accepted            bool
	Reason              string
}

// Verifier authenticates and parses a raw webhook delivery carrying a
// carrier's interception decision. Implementations must check the
// carrier's signature scheme before returning a DecisionEvent a caller can
// act on.
type Verifier interface {
	Verify(payload []byte, signatureHeader string) (DecisionEvent, error)
}

// AddressCheckInput is an address a carrier is asked to confirm it can
// deliver to and collect from (PW-042: a shop's return destination).
type AddressCheckInput struct {
	RecipientName string
	Phone         string
	Province      string
	District      string
	Ward          string
	StreetAddress string
}

// AddressCheckResult is the carrier's answer. Deliverable false comes with
// the carrier's reason; Reference identifies the check with the carrier.
type AddressCheckResult struct {
	Deliverable bool
	Reason      string
	Reference   string
}

// ErrAddressCheckUnsupported: the carrier offers no address check; an
// operator verifies the address instead.
var ErrAddressCheckUnsupported = errors.New("carrier: address check not supported")

// AddressChecker asks a carrier's address validation API.
type AddressChecker interface {
	CheckAddress(ctx context.Context, in AddressCheckInput) (AddressCheckResult, error)
}
