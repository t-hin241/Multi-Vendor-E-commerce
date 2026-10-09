package domain_test

import (
	"testing"

	"shopee/backend/services/shipment/internal/domain"
)

// PW-010: Shipment acknowledges the fee basis and quote validity it runs.
func TestShipmentRuleReadiness(t *testing.T) {
	for _, c := range [][2]string{{domain.RuleShippingFeeBasis, domain.FeeBasisCheckout}, {domain.RuleQuoteValidity, "quote-15m"}} {
		if ready, hash, reason := domain.RuleReadiness(c[0], c[1]); !ready || len(hash) != 64 || reason != "" {
			t.Fatalf("%v must be ready: %v %q %q", c, ready, hash, reason)
		}
	}
	for _, c := range [][2]string{{domain.RuleQuoteValidity, "quote-60m"}, {domain.RuleShippingFeeBasis, "recomputed"}, {"shipment.free_shipping", "yes"}} {
		if ready, _, reason := domain.RuleReadiness(c[0], c[1]); ready || reason == "" {
			t.Fatalf("%v must not be ready", c)
		}
	}
}
