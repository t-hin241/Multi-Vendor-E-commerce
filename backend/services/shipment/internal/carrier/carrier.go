// Package carrier defines Shipment's boundary with an external carrier for
// delivery interception. Domain and usecase code depend only on these
// interfaces, never on a concrete carrier SDK — swapping the mock adapter
// used today for a real one later means adding a new implementation of this
// package, not touching business logic. Mirrors
// payment/internal/provider's Provider/Verifier split.
package carrier

import "context"

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
