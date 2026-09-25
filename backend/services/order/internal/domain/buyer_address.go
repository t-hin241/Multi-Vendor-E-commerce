package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// BuyerAddress is one of a buyer's saved shipping addresses. A buyer may
// have several; exactly one is marked default and pre-selected at
// checkout.
type BuyerAddress struct {
	ID            string
	BuyerID       string
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
