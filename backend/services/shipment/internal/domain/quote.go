package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

// FeeCurrency is the currency every fee rule is entered in. Fee rules carry
// no currency of their own, so quotes state it explicitly and Order refuses
// a quote whose currency differs from the order's.
const FeeCurrency = "VND"

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
}

// QuotedFee is a quote Order already snapshotted and paid for; a shipment
// created later uses it instead of re-pricing with a newer rule.
type QuotedFee struct {
	FeeAmount int64
	CarrierID string
	ZoneID    string
	FeeRuleID string
}

// ValidateQuoteInput checks what a quote needs before any lookup.
func ValidateQuoteInput(vendorID, province string, weightGrams int64) error {
	if vendorID == "" || province == "" {
		return apperror.Validation("vendor_id and province are required")
	}
	if weightGrams < 0 {
		return apperror.Validation("Package weight cannot be negative")
	}
	return nil
}
