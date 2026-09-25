package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// Carrier is an admin-managed reference entity (e.g. GHN, GHTK, Viettel
// Post) — plain CRUD, not versioned, since it's a structural identity, not
// a rule whose historical value needs preserving.
type Carrier struct {
	ID        string
	Name      string
	Code      string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func ValidateCarrier(name, code string) error {
	if strings.TrimSpace(name) == "" {
		return apperror.Validation("Carrier name is required")
	}
	if strings.TrimSpace(code) == "" {
		return apperror.Validation("Carrier code is required")
	}
	return nil
}
