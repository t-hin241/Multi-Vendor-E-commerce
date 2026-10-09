package domain

import "shopee/backend/pkg/policyrules"

// Rules Payment enforces and may acknowledge for a published policy
// (AF-02, PW-010).
const (
	// RulePayoutMethod: how a shop is paid. Only manual bank transfers to
	// the shop's verified payout destination, in admin-made batches, exist.
	RulePayoutMethod   = "payment.payout_method"
	PayoutManualBank   = "manual-bank-transfer"
	ruleImplementation = "payment-policy-rules-v1"
)

// RuleReadiness tells Vendor whether Payment enforces this rule version.
func RuleReadiness(key, value string) (bool, string, string) {
	switch key {
	case RulePayoutMethod:
		if value != PayoutManualBank {
			return false, "", "Payment pays shops only by manual bank transfer (\"" + PayoutManualBank + "\")"
		}
	default:
		return false, "", "Payment does not own rule " + key
	}
	return true, policyrules.Hash(ruleImplementation, key, value), ""
}
