package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// Zone groups provinces for fee-rule matching. Plain CRUD, not versioned —
// only the fee rule attached to a (carrier, zone) pair needs a historical
// snapshot; the zone's own definition and its province membership are
// structural, and a shipment already snapshots the zone name it used at
// creation time regardless of later reassignment.
type Zone struct {
	ID        string
	Name      string
	Code      string
	CreatedAt time.Time
}

func ValidateZone(name, code string) error {
	if strings.TrimSpace(name) == "" {
		return apperror.Validation("Zone name is required")
	}
	if strings.TrimSpace(code) == "" {
		return apperror.Validation("Zone code is required")
	}
	return nil
}

func ValidateProvinceCode(provinceCode string) error {
	if strings.TrimSpace(provinceCode) == "" {
		return apperror.Validation("Province code is required")
	}
	return nil
}
