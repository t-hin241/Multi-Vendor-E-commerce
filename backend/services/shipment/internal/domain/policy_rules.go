package domain

import (
	"fmt"

	"shopee/backend/pkg/policyrules"
)

// Rules Shipment enforces and may acknowledge for a published policy
// (AF-02, PW-010).
const (
	// RuleShippingFeeBasis: the buyer pays the fee quoted at checkout,
	// never a fee recomputed later.
	RuleShippingFeeBasis = "shipment.fee_basis"
	FeeBasisCheckout     = "checkout-quote"
	// RuleQuoteValidity: how long a shipping quote is honoured
	// ("quote-<minutes>m", QuoteTTL).
	RuleQuoteValidity  = "shipment.quote_validity"
	ruleImplementation = "shipment-policy-rules-v1"
)

// RuleReadiness tells Vendor whether Shipment enforces this rule version.
func RuleReadiness(key, value string) (bool, string, string) {
	switch key {
	case RuleShippingFeeBasis:
		if value != FeeBasisCheckout {
			return false, "", "Shipment charges the fee quoted at checkout (\"" + FeeBasisCheckout + "\")"
		}
	case RuleQuoteValidity:
		if want := fmt.Sprintf("quote-%dm", int(QuoteTTL.Minutes())); value != want {
			return false, "", "Shipment honours a quote for " + want
		}
	default:
		return false, "", "Shipment does not own rule " + key
	}
	return true, policyrules.Hash(ruleImplementation, key, value), ""
}
