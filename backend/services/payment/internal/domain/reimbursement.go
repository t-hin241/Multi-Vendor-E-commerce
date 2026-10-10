package domain

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// PW-032: a reimbursement is money the marketplace pays a buyer outside a
// capture (a return shipping fee the buyer paid, a goodwill gesture). It
// is its own book: never a refund, never counted against what a capture
// can still refund, never charged to a shop's settlement. One admin
// prepares it, a different one approves it, then the bank transfer to the
// buyer's verified refund account is recorded.

type ReimbursementStatus string

const (
	ReimbursementRequested ReimbursementStatus = "requested"
	ReimbursementApproved  ReimbursementStatus = "approved"
	ReimbursementPaid      ReimbursementStatus = "paid"
	ReimbursementRejected  ReimbursementStatus = "rejected"
)

// ReimbursementReasons a reimbursement may be for.
var ReimbursementReasons = map[string]bool{"return_shipping_fee": true, "goodwill": true, "other": true}

// ProofPurposeReimbursementDecide is the reauthentication purpose of an
// approval or rejection.
const ProofPurposeReimbursementDecide = "payment.reimbursement.decide"

type Reimbursement struct {
	ID             string
	OrderID        string
	BuyerID        string
	ReasonCode     string
	Reason         string
	Amount         int64
	Currency       string
	Status         ReimbursementStatus
	RequestedBy    string
	DecidedBy      *string
	DecidedAt      *time.Time
	DecisionReason *string
	DestinationID  *string
	// DestinationMasked is the account the transfer went to, as shown.
	DestinationMasked *string
	BankReference     *string
	BankReferenceKey  *string
	PaidBy            *string
	PaidAt            *time.Time
	IdempotencyKey    *string
	Version           int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ReimbursementInput is what an admin prepares.
type ReimbursementInput struct {
	OrderID        string
	ReasonCode     string
	Reason         string
	Amount         int64
	Currency       string
	IdempotencyKey string
}

// ValidateReimbursement checks a request against the per-reimbursement cap.
func ValidateReimbursement(in ReimbursementInput, maxAmount int64) (ReimbursementInput, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case !ReimbursementReasons[in.ReasonCode]:
		return in, apperror.Validation("reason_code must be return_shipping_fee, goodwill or other")
	case in.Reason == "" || len([]rune(in.Reason)) > 500:
		return in, apperror.Validation("A reason of 1-500 characters is required")
	case in.Amount <= 0:
		return in, apperror.Validation("The amount must be positive")
	case maxAmount > 0 && in.Amount > maxAmount:
		return in, apperror.Validation("The amount is above the reimbursement limit of " + strconv.FormatInt(maxAmount, 10))
	case !currencyFormat.MatchString(in.Currency):
		return in, apperror.Validation("currency must be a 3-letter code")
	}
	return in, nil
}

var (
	ErrReimbursementChanged = manualError(http.StatusConflict, "stale_reimbursement", "This reimbursement changed meanwhile; reload and try again")
	ErrReimbursementSelf    = manualError(http.StatusConflict, "self_approval", "The admin who prepared a reimbursement cannot approve it")
	ErrNoVerifiedAccount    = manualError(http.StatusConflict, "destination_not_verified",
		"The buyer has no verified refund account for this order; the reimbursement waits until one is verified")
	ErrReimbursementsOff = manualError(http.StatusNotFound, "feature_disabled", "Reimbursements are not enabled")
)

// ReimbursementDecisionRef names one approval or rejection of one version.
func ReimbursementDecisionRef(id string, version int, approve bool) string {
	return "reimbursement:" + id + ":v" + strconv.Itoa(version) + ":" + pick(approve, "approve", "reject")
}

// Decide approves or rejects a requested reimbursement; never by the admin
// who prepared it.
func (r *Reimbursement) Decide(actor string, approve bool, reason string, now time.Time) error {
	if r.Status != ReimbursementRequested {
		return manualError(http.StatusConflict, "stale_reimbursement", "Only a requested reimbursement can be decided; it is "+string(r.Status))
	}
	if actor == r.RequestedBy {
		return ErrReimbursementSelf
	}
	r.Status = ReimbursementRejected
	if approve {
		r.Status = ReimbursementApproved
	}
	r.DecidedBy, r.DecidedAt, r.DecisionReason = &actor, &now, &reason
	return nil
}

// RecordPayment records the bank transfer of an approved reimbursement to
// the buyer's verified account (destinationID).
func (r *Reimbursement) RecordPayment(actor, bankReference, destinationID string, now time.Time) error {
	if r.Status != ReimbursementApproved {
		return manualError(http.StatusConflict, "stale_reimbursement", "Only an approved reimbursement can be paid; it is "+string(r.Status))
	}
	ref := strings.TrimSpace(bankReference)
	key := BankReferenceKey(ref)
	if len(ref) < 3 || len(ref) > 100 || len(key) < 3 {
		return apperror.Validation("Bank reference must be 3-100 characters")
	}
	r.Status, r.BankReference, r.BankReferenceKey, r.DestinationID, r.PaidBy, r.PaidAt = ReimbursementPaid, &ref, &key, &destinationID, &actor, &now
	return nil
}
