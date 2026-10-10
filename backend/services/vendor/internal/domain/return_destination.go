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
	// CarrierCheck is the carrier's last address check (PW-042).
	CarrierCheck *CarrierAddressCheck
}

// Carrier address check results (PW-042).
const (
	CarrierDeliverable   = "deliverable"
	CarrierUndeliverable = "undeliverable"
	CarrierUnsupported   = "unsupported"
)

// CarrierAddressCheck is the carrier's answer for one destination version.
type CarrierAddressCheck struct {
	Version   int64
	Result    string
	Reason    *string
	Reference *string
	CheckedAt time.Time
}

// Verified: an admin, or the carrier's address check, checked this exact
// version.
func (d *ReturnDestination) Verified() bool {
	return d.VerifiedVersion != nil && *d.VerifiedVersion == d.Version
}

// VerifiedByCarrier: the current version was verified by the carrier's
// address check, not by a person.
func (d *ReturnDestination) VerifiedByCarrier() bool {
	return d.Verified() && d.VerifiedBy == nil
}

// CurrentCarrierCheck is the carrier's check of the current version.
func (d *ReturnDestination) CurrentCarrierCheck() *CarrierAddressCheck {
	if d.CarrierCheck == nil || d.CarrierCheck.Version != d.Version {
		return nil
	}
	return d.CarrierCheck
}

// AwaitsDecision: nobody verified or rejected the current version.
func (d *ReturnDestination) AwaitsDecision() bool {
	return !d.Verified() && d.RejectionReason == nil
}

// ValidateReceivingHours: when the shop accepts parcels, shown to buyers.
func ValidateReceivingHours(hours string) (string, error) {
	hours = strings.TrimSpace(hours)
	if hours == "" || len(hours) > 200 {
		return "", apperror.Validation("Receiving hours are required (at most 200 characters)")
	}
	return hours, nil
}
