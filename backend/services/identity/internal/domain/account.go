package domain

import (
	"strings"
	"time"
	"unicode"

	"shopee/backend/pkg/apperror"
)

type ProfileUpdate struct {
	FullName        string `json:"full_name"`
	Phone           string `json:"phone"`
	ExpectedVersion int64  `json:"expected_version"`
}

func AccountValidation(message string) error {
	return &apperror.Error{Code: apperror.CodeValidation, Status: 422, Message: message}
}

func (p *ProfileUpdate) Validate() error {
	p.FullName, p.Phone = strings.TrimSpace(p.FullName), strings.TrimSpace(p.Phone)
	if p.FullName == "" || len(p.FullName) > 200 || strings.IndexFunc(p.FullName, unicode.IsControl) >= 0 {
		return AccountValidation("full_name: enter a name of at most 200 bytes without control characters")
	}
	if p.ExpectedVersion < 1 {
		return AccountValidation("expected_version: must be positive")
	}
	if p.Phone == "" {
		return nil
	}
	digits := strings.TrimPrefix(p.Phone, "+")
	if len(digits) < 7 || len(digits) > 15 || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return AccountValidation("phone: use 7 to 15 digits, optionally starting with +")
	}
	return nil
}

type AccountSession struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	Current      bool      `json:"current"`
}

type SessionPage struct {
	Items      []AccountSession `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type EmailChange struct {
	ID, UserID       string
	TargetCiphertext []byte
	AuthVersion      int64
	ExpiresAt        time.Time
	UsedAt           *time.Time
}
