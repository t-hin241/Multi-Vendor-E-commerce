package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

// FeeRule is insert-only and versioned per (CarrierID, ZoneID), mirroring
// catalog's category_attribute_rules: editing a rule inserts version+1,
// never updates/deletes the old row, so a shipment already created against
// an older version keeps its snapshot meaning intact.
type FeeRule struct {
	ID              string
	CarrierID       string
	ZoneID          string
	Version         int
	BaseFeeAmount   int64
	FreeWeightGrams int64
	ExtraFeePerKg   int64
	CreatedBy       *string
	CreatedAt       time.Time
}

func ValidateFeeRule(baseFeeAmount, freeWeightGrams, extraFeePerKg int64) error {
	if baseFeeAmount < 0 {
		return apperror.Validation("Base fee amount cannot be negative")
	}
	if freeWeightGrams < 0 {
		return apperror.Validation("Free weight allowance cannot be negative")
	}
	if extraFeePerKg < 0 {
		return apperror.Validation("Extra fee per kg cannot be negative")
	}
	return nil
}

// ComputeShippingFee applies a fee rule to a package's weight: a flat base
// fee, plus an extra per-kg charge on whatever weight exceeds the rule's
// free allowance, rounded up to the next whole kg. Pure integer math —
// money and weight are both fixed-point minor units (cents, grams), never
// float, matching the rest of the codebase's money handling.
func ComputeShippingFee(rule FeeRule, weightGrams int64) int64 {
	billable := weightGrams - rule.FreeWeightGrams
	if billable <= 0 {
		return rule.BaseFeeAmount
	}
	billableKg := (billable + 999) / 1000 // round up to the next whole kg
	return rule.BaseFeeAmount + billableKg*rule.ExtraFeePerKg
}
