// Package manual is the carrier adapter for MVP manual fulfillment (D04):
// no carrier API is called. An interception request is recorded for an
// operator, who contacts the carrier and records the decision as an audited
// action. There is no carrier webhook in this mode.
package manual

import (
	"context"
	"errors"

	"shopee/backend/services/shipment/internal/carrier"
)

type Provider struct{}

// RequestInterception only creates a reference; the decision is recorded
// by an operator after calling the carrier.
func (Provider) RequestInterception(_ context.Context, in carrier.RequestInterceptionInput) (carrier.RequestInterceptionResult, error) {
	return carrier.RequestInterceptionResult{ProviderReferenceID: "manual:" + in.ShipmentID}, nil
}

// ErrNoWebhook: manual mode has no carrier callbacks.
var ErrNoWebhook = errors.New("manual carrier mode accepts no webhooks")

func (Provider) Verify([]byte, string) (carrier.DecisionEvent, error) {
	return carrier.DecisionEvent{}, ErrNoWebhook
}

// CheckAddress: manual mode calls no carrier API; an admin verifies.
func (Provider) CheckAddress(context.Context, carrier.AddressCheckInput) (carrier.AddressCheckResult, error) {
	return carrier.AddressCheckResult{}, carrier.ErrAddressCheckUnsupported
}
