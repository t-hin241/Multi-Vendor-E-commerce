package domain

import (
	"regexp"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// VendorAddress is one of a vendor's warehouse/pickup addresses. A vendor
// may have several; exactly one is marked default.
type VendorAddress struct {
	ID            string
	VendorID      string
	RecipientName string
	Phone         string
	Province      string
	District      string
	Ward          string
	StreetAddress string
	IsDefault     bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func ValidateAddress(recipientName, phone, province, district, ward, streetAddress string) error {
	for _, part := range []string{recipientName, province, district, ward, streetAddress} {
		if len(strings.TrimSpace(part)) > 300 {
			return apperror.Validation("Address fields must not exceed 300 bytes")
		}
	}
	if !regexp.MustCompile(`^\+?[0-9]{7,15}$`).MatchString(strings.TrimSpace(phone)) {
		return apperror.Validation("Phone must contain 7 to 15 digits, optionally prefixed with +")
	}
	if strings.TrimSpace(recipientName) == "" {
		return apperror.Validation("Recipient name is required")
	}
	if strings.TrimSpace(phone) == "" {
		return apperror.Validation("Phone number is required")
	}
	if strings.TrimSpace(province) == "" {
		return apperror.Validation("Province is required")
	}
	if strings.TrimSpace(district) == "" {
		return apperror.Validation("District is required")
	}
	if strings.TrimSpace(ward) == "" {
		return apperror.Validation("Ward is required")
	}
	if strings.TrimSpace(streetAddress) == "" {
		return apperror.Validation("Street address is required")
	}
	return nil
}
