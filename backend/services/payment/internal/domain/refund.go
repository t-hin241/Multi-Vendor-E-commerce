package domain

import (
	"regexp"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type RefundStatus string

const (
	// RefundAwaitingProvider: accepted and counted against the capture, but
	// money is not returned until an operator records the provider or
	// manual receipt.
	RefundAwaitingProvider RefundStatus = "awaiting_provider_refund"
	RefundPending          RefundStatus = "pending"
	RefundSucceeded        RefundStatus = "succeeded"
	RefundFailed           RefundStatus = "failed"
)

// Open refunds still hold part of the captured amount.
func (s RefundStatus) Open() bool { return s == RefundAwaitingProvider || s == RefundPending }

// Refund returns part or all of one captured payment intent.
type Refund struct {
	ID                string
	PaymentIntentID   string
	OrderID           string
	OrderRefundID     *string
	VendorOrderID     *string
	Amount            int64
	Currency          string
	Reason            string
	Status            RefundStatus
	RequestedBy       string
	EvidenceReference *string
	Note              *string
	FailureReason     *string
	ResolvedBy        *string
	ResolvedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// RefundRequest is Order asking for money back. OrderRefundID is the
// idempotency key; PaymentID pins a specific capture (a rejected late or
// duplicate payment), otherwise the order's single capture is used.
type RefundRequest struct {
	OrderRefundID string
	OrderID       string
	PaymentID     *string
	// VendorOrderID names the package the refund charges, when there is one.
	VendorOrderID *string
	Amount        int64
	Currency      string
	Reason        string
	RequestedBy   string
}

var currencyFormat = regexp.MustCompile(`^[A-Z]{3}$`)

func ValidateRefundRequest(r RefundRequest) error {
	if r.Amount <= 0 {
		return apperror.Validation("Refund amount must be positive")
	}
	if !currencyFormat.MatchString(r.Currency) {
		return apperror.Validation("Refund currency must be a 3-letter code")
	}
	if n := len(strings.TrimSpace(r.Reason)); n == 0 || n > 500 {
		return apperror.Validation("Refund reason must be 1-500 characters")
	}
	return nil
}

// Same reports whether a replayed request asks for exactly this refund.
func (r *Refund) Same(req RefundRequest) bool {
	return r.OrderID == req.OrderID && r.Amount == req.Amount && r.Currency == req.Currency &&
		(req.PaymentID == nil || *req.PaymentID == r.PaymentIntentID)
}

// CheckRefundable enforces that a refund draws on a captured intent in its
// currency and that every non-failed refund together never exceeds the
// captured amount. committed is the sum of the intent's non-failed refunds.
func CheckRefundable(intent *PaymentIntent, committed int64, req RefundRequest) error {
	if intent.Status != StatusCaptured && intent.Status != StatusRefunded {
		return apperror.Conflict("Only a captured payment can be refunded")
	}
	if intent.Currency != req.Currency {
		return apperror.Conflict("Refund currency does not match the payment")
	}
	if committed < 0 || req.Amount > intent.Amount-committed {
		return apperror.Conflict("Refund exceeds the captured amount still refundable")
	}
	return nil
}

// RefundResolution is an operator recording what happened to the money.
type RefundResolution struct {
	Outcome           RefundStatus
	EvidenceReference string
	Note              string
}

// Resolve applies an operator's resolution. A succeeded refund needs the
// provider or bank reference proving money left; a failed one needs the
// reason. Repeating the same resolution is a no-op; changing a terminal
// outcome is refused.
func (r *Refund) Resolve(res RefundResolution, actorID string, now time.Time) (changed bool, err error) {
	evidence := strings.TrimSpace(res.EvidenceReference)
	note := strings.TrimSpace(res.Note)
	switch res.Outcome {
	case RefundSucceeded:
		if evidence == "" || len(evidence) > 200 {
			return false, apperror.Validation("A succeeded refund needs a provider or bank reference (max 200 characters)")
		}
	case RefundFailed:
		if note == "" {
			return false, apperror.Validation("A failed refund needs the failure reason")
		}
	default:
		return false, apperror.Validation("Outcome must be succeeded or failed")
	}
	if len(note) > 500 {
		return false, apperror.Validation("Note must be at most 500 characters")
	}
	if !r.Status.Open() {
		if r.Status == res.Outcome {
			return false, nil
		}
		return false, apperror.Conflict("Refund is already " + string(r.Status))
	}
	r.Status = res.Outcome
	r.ResolvedBy, r.ResolvedAt = &actorID, &now
	if evidence != "" {
		r.EvidenceReference = &evidence
	}
	if note != "" {
		r.Note = &note
	}
	if res.Outcome == RefundFailed {
		r.FailureReason = &note
	}
	return true, nil
}

// RefundOutcome is what Order learns about a resolved refund.
type RefundOutcome struct {
	OrderRefundID   string `json:"order_refund_id"`
	PaymentRefundID string `json:"payment_refund_id"`
	Status          string `json:"status"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	FailureReason   string `json:"failure_reason,omitempty"`
}
