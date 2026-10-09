package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// ReturnDestination is where a shop receives returned goods (AF-05): one
// of its addresses, designated by the owner, verified by an admin for
// exactly this version. Any change to the designation or the address
// bumps the version and needs a new verification.
type ReturnDestination struct {
	VendorID        string
	AddressID       string
	ReceivingHours  string
	Version         int64
	VerifiedVersion *int64
	VerifiedBy      *string
	VerifiedAt      *time.Time
	RejectionReason *string
	UpdatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Address is the designated address as it is now.
	Address *VendorAddress
}

// Verified: an admin checked this exact version.
func (d *ReturnDestination) Verified() bool {
	return d.VerifiedVersion != nil && *d.VerifiedVersion == d.Version
}

// ValidateReceivingHours: when the shop accepts parcels, shown to buyers.
func ValidateReceivingHours(hours string) (string, error) {
	hours = strings.TrimSpace(hours)
	if hours == "" || len(hours) > 200 {
		return "", apperror.Validation("Receiving hours are required (at most 200 characters)")
	}
	return hours, nil
}
