package domain

import (
	"fmt"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type ReturnRequestStatus string

const (
	ReturnRequested       ReturnRequestStatus = "requested"
	ReturnVendorConfirmed ReturnRequestStatus = "vendor_confirmed"
	ReturnRejected        ReturnRequestStatus = "rejected"
	// ReturnApproved: admin accepted the return; goods not received yet.
	ReturnApproved ReturnRequestStatus = "approved"
	// ReturnReceived: goods received and inspected; refund about to be requested.
	ReturnReceived      ReturnRequestStatus = "received"
	ReturnRefundPending ReturnRequestStatus = "refund_pending"
	ReturnRefunded      ReturnRequestStatus = "refunded"
	ReturnRefundFailed  ReturnRequestStatus = "refund_failed"
)

type ReturnRequest struct {
	ID                string
	OrderID           string
	OrderItemID       string
	BuyerID           string
	Reason            string
	Status            ReturnRequestStatus
	Quantity          int64
	RefundAmount      int64
	PolicyVersion     string
	ReturnWindowDays  *int
	Evidence          *string
	VendorNote        *string
	VendorConfirmedBy *string
	VendorConfirmedAt *time.Time
	DecidedBy         *string
	DecisionNote      *string
	DecidedAt         *time.Time
	ReceivedBy        *string
	ReceivedAt        *time.Time
	InspectionNote    *string
	Restock           *bool
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ReturnEvent is one audited step of a return request.
type ReturnEvent struct {
	ID          string
	ReturnID    string
	ActorUserID *string
	ActorRole   string
	Action      string
	FromStatus  *string
	ToStatus    string
	Note        *string
	CreatedAt   time.Time
}

// ReturnPolicy is the return rule in force when a request is created; its
// version and window are copied onto the request.
type ReturnPolicy struct {
	Version    string
	WindowDays int
}

// returnTransitions lists who may move a request where. Refund states are
// driven only by Payment outcomes.
var returnTransitions = map[ReturnRequestStatus][]ReturnRequestStatus{
	ReturnRequested:       {ReturnVendorConfirmed, ReturnApproved, ReturnRejected},
	ReturnVendorConfirmed: {ReturnApproved, ReturnRejected},
	ReturnApproved:        {ReturnReceived},
	ReturnReceived:        {ReturnRefundPending},
	ReturnRefundPending:   {ReturnRefunded, ReturnRefundFailed},
	ReturnRefundFailed:    {ReturnRefundPending},
}

func CanTransitionReturn(from, to ReturnRequestStatus) bool {
	for _, allowed := range returnTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ReturnEligibility is what Order checks before accepting a return.
type ReturnEligibility struct {
	VendorOrderStatus Status
	CompletedAt       *time.Time
	ItemQuantity      int64
	ItemPrice         int64
	// AlreadyReturned is the quantity of the item in earlier requests that
	// were not rejected.
	AlreadyReturned int64
	Now             time.Time
}

// ValidateNewReturn applies the policy: only delivered (completed) items,
// only inside the return window, for at most the purchased quantity not
// already in a non-rejected return. It
// returns the refund amount for the returned units (shipping excluded).
func ValidateNewReturn(policy ReturnPolicy, e ReturnEligibility, quantity int64, reason string, evidence *string) (int64, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 2000 {
		return 0, apperror.Validation("Return reason must be between 1 and 2000 characters")
	}
	if evidence != nil && len(*evidence) > 2000 {
		return 0, apperror.Validation("Evidence must be at most 2000 characters")
	}
	if e.VendorOrderStatus != StatusCompleted || e.CompletedAt == nil {
		return 0, apperror.Conflict("Only delivered (completed) items can be returned")
	}
	if e.Now.After(e.CompletedAt.Add(time.Duration(policy.WindowDays) * 24 * time.Hour)) {
		return 0, apperror.Conflict("The return window for this item has ended")
	}
	remaining := e.ItemQuantity - e.AlreadyReturned
	if remaining <= 0 {
		return 0, apperror.Conflict("Every unit of this item is already in a return request")
	}
	if quantity <= 0 || quantity > remaining {
		return 0, apperror.Validation(fmt.Sprintf("Return quantity must be between 1 and %d", remaining))
	}
	amount, ok := MulAmount(e.ItemPrice, quantity)
	if !ok {
		return 0, apperror.Validation("Return amount is too large")
	}
	return amount, nil
}

// ValidateNote trims an optional free-text note to its limit.
func ValidateNote(note string, max int, required bool, field string) (*string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		if required {
			return nil, apperror.Validation(field + " is required")
		}
		return nil, nil
	}
	if len(note) > max {
		return nil, apperror.Validation(field + " is too long")
	}
	return &note, nil
}
