package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// Refund is Order's request to give money back, carried out by Payment.
// Money only counts as returned once Payment confirms succeeded.
type Refund struct {
	ID              string
	OrderID         string
	VendorOrderID   *string
	ReturnRequestID *string
	PaymentID       *string
	ReasonCode      string
	Amount          int64
	Currency        string
	Reason          string
	Status          RefundStatus
	PaymentRefundID *string
	FailureReason   *string
	RequestedBy     string
	// IdempotencyKey makes an admin's resend of the same request return
	// the refund already created instead of a second one.
	IdempotencyKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ResolvedAt     *time.Time
}

type RefundStatus string

const (
	RefundRequested RefundStatus = "requested"
	RefundSubmitted RefundStatus = "submitted"
	RefundSucceeded RefundStatus = "succeeded"
	RefundFailed    RefundStatus = "failed"
	RefundRejected  RefundStatus = "rejected"
)

const (
	RefundReasonReturn           = "return"
	RefundReasonDispute          = "dispute"
	RefundReasonLatePayment      = "late_payment"
	RefundReasonDuplicatePayment = "duplicate_payment"
)

// Open reports whether the refund still reserves part of the refundable
// amount (requested, submitted or already paid out).
func (s RefundStatus) Open() bool {
	return s == RefundRequested || s == RefundSubmitted || s == RefundSucceeded
}

// Terminal reports whether Payment's outcome is final.
func (s RefundStatus) Terminal() bool {
	return s == RefundSucceeded || s == RefundFailed || s == RefundRejected
}

// CanApplyOutcome reports whether a Payment outcome may move a refund from
// status from to to; replaying the same terminal outcome is allowed.
func CanApplyOutcome(from, to RefundStatus) bool {
	if from == to && to.Terminal() {
		return true
	}
	switch to {
	case RefundSubmitted:
		return from == RefundRequested
	case RefundSucceeded, RefundFailed:
		return from == RefundRequested || from == RefundSubmitted
	case RefundRejected:
		return from == RefundRequested
	}
	return false
}

// ValidateRefundRequest checks the amount against what can still be
// refunded (captured money minus refunds already open or paid).
func ValidateRefundRequest(amount, refundable int64, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return "", apperror.Validation("A refund reason of at most 500 characters is required")
	}
	if amount <= 0 {
		return "", apperror.Validation("Refund amount must be positive")
	}
	if amount > refundable {
		return "", apperror.Conflict("Refund amount exceeds what can still be refunded")
	}
	return reason, nil
}

// RefundOutcome is Payment's confirmed result for one refund.
type RefundOutcome struct {
	RefundID        string
	PaymentRefundID string
	Status          RefundStatus
	Amount          int64
	Currency        string
	FailureReason   string
}
