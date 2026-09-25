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
	LogoURL         *string
	LogoObjectKey   *string
	BannerURL       *string
	BannerObjectKey *string
	PolicyText      string
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

const maxImageBytes = 5 * 1024 * 1024

var allowedImageContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// ValidateImageUpload checks content type and size before anything is sent
// to object storage, and returns the file extension to use for the stored
// object key. Same rule as Catalog's product-image upload.
func ValidateImageUpload(contentType string, size int64) (extension string, err error) {
	ext, ok := allowedImageContentTypes[contentType]
	if !ok {
		return "", apperror.Validation("Image must be JPEG, PNG or WebP")
	}
	if size <= 0 || size > maxImageBytes {
		return "", apperror.Validation("Image must be no larger than 5MB")
	}
	return ext, nil
}

// AuditLog is one recorded moderation decision on a vendor — written once
// at Approve/Reject time and never edited, so this is also the full
// decision history a later admin can review.
type AuditLog struct {
	ActorUserID string
	Action      string
	Reason      *string
	CreatedAt   time.Time
}
