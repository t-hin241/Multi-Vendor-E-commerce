package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

// FeeCurrency is the currency every fee rule is entered in. Fee rules carry
// no currency of their own, so quotes state it explicitly and Order refuses
// a quote whose currency differs from the order's.
const FeeCurrency = "VND"

// QuoteTTL is how long Order may use a quote; it re-quotes at checkout.
const QuoteTTL = 15 * time.Minute

// Quote is a shipping fee for one vendor's package to one destination,
// computed from the vendor's default carrier and the current fee rule. It
// does not create a shipment; Order snapshots it into the order.
type Quote struct {
	VendorID           string
	FeeAmount          int64
	Currency           string
	CarrierID          string
	ZoneID             string
	ZoneName           string
	FeeRuleID          string
	FeeRuleVersion     int
	PackageWeightGrams int64
	QuotedAt           time.Time
	ExpiresAt          time.Time
}

// QuotedFee is a quote Order already snapshotted and paid for; a shipment
// created later uses it instead of re-pricing with a newer rule.
type QuotedFee struct {
	FeeAmount int64
	CarrierID string
	ZoneID    string
	FeeRuleID string
}

// maxPackageWeightGrams bounds a quote (1 tonne).
const maxPackageWeightGrams = 1_000_000

// ValidateQuoteInput checks what a quote needs before any lookup. A
// missing (zero) weight cannot be priced: shipping is unavailable rather
// than charged at the base fee.
func ValidateQuoteInput(vendorID, province string, weightGrams int64) error {
	if vendorID == "" || province == "" {
		return apperror.Validation("vendor_id and province are required")
	}
	if weightGrams <= 0 {
		return apperror.Validation("Package weight is missing for this shop's products, so shipping cannot be priced")
	}
	if weightGrams > maxPackageWeightGrams {
		return apperror.Validation("Package is too heavy to ship")
	}
	return nil
}
