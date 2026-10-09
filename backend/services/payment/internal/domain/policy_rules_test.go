package domain_test

import (
	"testing"

	"shopee/backend/services/payment/internal/domain"
)

// PW-010: Payment acknowledges only the payout method it runs.
func TestPaymentRuleReadiness(t *testing.T) {
	ready, hash, _ := domain.RuleReadiness(domain.RulePayoutMethod, domain.PayoutManualBank)
	if !ready || len(hash) != 64 {
		t.Fatalf("manual bank transfer must be ready with a hash: %v %q", ready, hash)
	}
	if again, h2, _ := domain.RuleReadiness(domain.RulePayoutMethod, domain.PayoutManualBank); !again || h2 != hash {
		t.Fatal("the hash is stable for the same rule and implementation")
	}
	for _, c := range [][2]string{{domain.RulePayoutMethod, "instant-payout"}, {"payment.settlement_delay", "7d"}} {
		if ready, hash, reason := domain.RuleReadiness(c[0], c[1]); ready || hash != "" || reason == "" {
			t.Fatalf("%v must not be ready", c)
		}
	}
}
