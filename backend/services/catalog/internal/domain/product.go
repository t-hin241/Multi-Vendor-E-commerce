package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	StatusPendingReview Status = "pending_review"
	StatusApproved      Status = "approved"
	StatusRejected      Status = "rejected"
)

type Product struct {
	ID              string
	VendorID        string
	CategoryID      string
	Name            string
	Slug            string
	Description     string
	PriceAmount     int64
	Currency        string
	Status          Status
	RejectionReason *string
	IsActive        bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IsPubliclyVisible mirrors the storefront visibility rule: a product only
// shows up once it has been approved, is currently active, and belongs to
// an approved vendor (checked separately by the caller, since vendor
// approval is owned by the Vendor service).
func (p *Product) IsPubliclyVisible() bool {
	return p.Status == StatusApproved && p.IsActive
}

// CanTransition enforces that only a pending_review product can be decided.
func CanTransition(from, to Status) bool {
	if from != StatusPendingReview {
		return false
	}
	return to == StatusApproved || to == StatusRejected
}

type ProductImage struct {
	ID        string
	ProductID string
	ObjectKey string
	URL       string
	Position  int
	CreatedAt time.Time
}

const (
	maxImageBytes  = 5 * 1024 * 1024
	minPriceAmount = 1
)

var allowedImageContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func ValidateProductInput(name, description string, priceAmount int64) error {
	if strings.TrimSpace(name) == "" {
		return apperror.Validation("Product name is required")
	}
	if priceAmount < minPriceAmount {
		return apperror.Validation("Price must be a positive amount")
	}
	return nil
}

// ValidateImageUpload checks content type and size before anything is sent
// to object storage, and returns the file extension to use for the stored
// object key.
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
