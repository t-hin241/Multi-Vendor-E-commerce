package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"shopee/backend/pkg/apperror"
)

type PayoutAccount struct {
	ID                       string    `json:"id"`
	VendorID                 string    `json:"vendor_id"`
	Version                  int64     `json:"version"`
	BankBIN                  string    `json:"bank_bin"`
	Last4                    string    `json:"last4"`
	Status                   string    `json:"status"`
	Default                  bool      `json:"is_default"`
	RejectionReason          *string   `json:"rejection_reason,omitempty"`
	CreatedAt                time.Time `json:"created_at"`
	NumberCipher, NameCipher []byte    `json:"-"`
}
type PayoutDetails struct {
	AccountID string `json:"account_id"`
	Version   int64  `json:"version"`
	BankBIN   string `json:"bank_bin"`
	Number    string `json:"account_number"`
	Name      string `json:"account_name"`
}

var bankPattern = regexp.MustCompile(`^[0-9]{6}$`)
var accountPattern = regexp.MustCompile(`^[0-9]{6,32}$`)

func ValidatePayout(bank, number, name string) error {
	if !bankPattern.MatchString(bank) {
		return apperror.Validation("Bank BIN must contain 6 digits")
	}
	if !accountPattern.MatchString(number) {
		return apperror.Validation("Account number must contain 6 to 32 digits")
	}
	if len(strings.TrimSpace(name)) < 2 || len(name) > 160 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return apperror.Validation("Invalid account holder name")
	}
	return nil
}
