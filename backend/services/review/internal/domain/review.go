package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type ReviewStatus string

const (
	ReviewPublished ReviewStatus = "published"
	ReviewHidden    ReviewStatus = "hidden"
	maxCommentRunes              = 2000
	maxReviewImages              = 5
)

type Review struct {
	ID, BuyerID, BuyerName, VendorID, ProductID, OrderItemID, VendorOrderID string
	Rating                                                                  int
	Comment                                                                 string
	Status                                                                  ReviewStatus
	HiddenReasonID, HiddenBy                                                *string
	HiddenAt                                                                *time.Time
	CreatedAt, UpdatedAt                                                    time.Time
}

type Image struct {
	ID, ReviewID, ObjectKey, URL, ContentType string
	SizeBytes                                 int64
	Position                                  int
	CreatedAt                                 time.Time
}

type Reply struct {
	ReviewID, VendorID, Message string
	CreatedAt, UpdatedAt        time.Time
}

type Reason struct {
	ID, Code, Label string
	Description     *string
	IsActive        bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ReportStatus string

const (
	ReportOpen     ReportStatus = "open"
	ReportResolved ReportStatus = "resolved"
)

type ReportDecision string

const (
	DecisionKeep ReportDecision = "keep"
	DecisionHide ReportDecision = "hide"
)

type Report struct {
	ID, ReviewID, ReportingVendorID, ReasonID, ReasonCode, ReasonLabel string
	Note                                                               *string
	Status                                                             ReportStatus
	Decision                                                           *ReportDecision
	ResolutionReasonID, ResolvedBy                                     *string
	ResolvedAt                                                         *time.Time
	ResolutionNote                                                     *string
	CreatedAt, UpdatedAt                                               time.Time
}

type Summary struct {
	RatingAverage float64
	RatingCount   int64
	Distribution  [5]int64
}

func ValidateReview(rating int, comment string) (string, error) {
	comment = strings.TrimSpace(comment)
	if rating < 1 || rating > 5 {
		return "", apperror.Validation("Rating must be between 1 and 5")
	}
	if comment == "" {
		return "", apperror.Validation("Review comment is required")
	}
	if len([]rune(comment)) > maxCommentRunes {
		return "", apperror.Validation("Review comment must be at most 2000 characters")
	}
	return comment, nil
}

func ValidateReply(message string) (string, error) {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > maxCommentRunes {
		return "", apperror.Validation("Reply must contain at most 2000 characters")
	}
	return message, nil
}

func ValidateImage(contentType string, size int64) (string, error) {
	extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
	ext, ok := extensions[contentType]
	if !ok {
		return "", apperror.Validation("Image must be JPEG, PNG or WebP")
	}
	if size <= 0 || size > 5*1024*1024 {
		return "", apperror.Validation("Image must be no larger than 5MB")
	}
	return ext, nil
}
func MaxImages() int { return maxReviewImages }
