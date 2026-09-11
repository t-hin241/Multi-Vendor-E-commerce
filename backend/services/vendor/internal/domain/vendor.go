// Package domain holds Vendor's entities and business rules: onboarding
// status and the transitions an application may legally make.
package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

type Vendor struct {
	ID              string
	UserID          string
	ShopName        string
	Description     string
	Status          Status
	RejectionReason *string
	ApprovedBy      *string
	ApprovedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (v *Vendor) IsApproved() bool {
	return v.Status == StatusApproved
}

// CanTransition enforces that only a pending application can be decided;
// an already-decided application can't be silently re-decided.
func CanTransition(from Status, to Status) bool {
	if from != StatusPending {
		return false
	}
	return to == StatusApproved || to == StatusRejected
}

func ValidateApplication(shopName string) error {
	if strings.TrimSpace(shopName) == "" {
		return apperror.Validation("Shop name is required")
	}
	return nil
}
