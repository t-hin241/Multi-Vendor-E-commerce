package domain

import (
	"net/http"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// PW-027: one admin creates a payout batch, an independent admin approves
// it before any transfer result is recorded; a batch over the limits or
// paying a destination for the first time needs a second, distinct
// approver.

// Payout batch approval states.
const (
	PayoutApprovalNotRequired    = "not_required" // batches created before PW-027
	PayoutAwaitingApproval       = "awaiting_approval"
	PayoutAwaitingSecondApproval = "awaiting_second_approval"
	PayoutApproved               = "approved"
	PayoutRejected               = "rejected"
	PayoutWithdrawn              = "withdrawn"
)

// Reasons a batch needs a second approver.
const (
	PayoutRiskBatchOverLimit  = "batch_total_over_limit"
	PayoutRiskItemOverLimit   = "item_over_limit"
	PayoutRiskNewDestination  = "new_destination"
	PayoutRiskForeignCurrency = "currency_without_limit"
)

// ProofPurposePayoutBatchApprove is the reauthentication purpose of an
// approval (AF-19 on).
const ProofPurposePayoutBatchApprove = "payment.payout_batch.approve"

// PayoutBatchApprovalRef is the operation an approval proof is bound to.
func PayoutBatchApprovalRef(batchID, state string) string {
	return "payout_batch:" + batchID + ":" + state
}

// PayoutApprovalPolicy holds the limits (minor units of Currency) above
// which a batch needs a second approver. Zero disables a limit.
type PayoutApprovalPolicy struct {
	Currency   string
	BatchLimit int64
	ItemLimit  int64
}

// Assess returns how many approvals a batch needs and why a second one is
// required. A batch in a currency the limits are not set for always needs
// two.
func (p PayoutApprovalPolicy) Assess(currency string, items []*PayoutItem, newDestinations int) (int, []string) {
	reasons := []string{}
	var total int64
	itemOver := false
	for _, it := range items {
		total += it.Amount
		if p.ItemLimit > 0 && it.Amount > p.ItemLimit {
			itemOver = true
		}
	}
	if !strings.EqualFold(currency, p.Currency) {
		reasons = append(reasons, PayoutRiskForeignCurrency)
	} else {
		if p.BatchLimit > 0 && total > p.BatchLimit {
			reasons = append(reasons, PayoutRiskBatchOverLimit)
		}
		if itemOver {
			reasons = append(reasons, PayoutRiskItemOverLimit)
		}
	}
	if newDestinations > 0 {
		reasons = append(reasons, PayoutRiskNewDestination)
	}
	if len(reasons) > 0 {
		return 2, reasons
	}
	return 1, reasons
}

// PayoutBatchApproval is a batch's approval record.
type PayoutBatchApproval struct {
	State             string
	RequiredApprovals int
	RiskReasons       []string
	FirstApprovedBy   *string
	FirstApprovedAt   *time.Time
	SecondApprovedBy  *string
	SecondApprovedAt  *time.Time
	DecidedBy         *string
	DecisionReason    *string
	DecidedAt         *time.Time
}

// Open: the batch waits for an approval.
func (a PayoutBatchApproval) Open() bool {
	return a.State == PayoutAwaitingApproval || a.State == PayoutAwaitingSecondApproval
}

// AllowsTransfers: transfer results may be recorded.
func (a PayoutBatchApproval) AllowsTransfers() bool {
	return a.State == PayoutApprovalNotRequired || a.State == PayoutApproved || a.State == ""
}

func payoutApprovalError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Status: status, Message: message}
}

var (
	ErrPayoutBatchNotApproved = payoutApprovalError(http.StatusConflict, "payout_batch_not_approved",
		"This payout batch is not approved yet; no transfer result can be recorded")
	ErrPayoutSelfApproval = payoutApprovalError(http.StatusConflict, "self_approval",
		"The admin who created a payout batch cannot approve it")
	ErrPayoutSameApprover = payoutApprovalError(http.StatusConflict, "same_approver",
		"The second approval must come from a different admin than the first")
	ErrPayoutApprovalClosed = payoutApprovalError(http.StatusConflict, "approval_closed",
		"This payout batch is not waiting for an approval")
	ErrPayoutApprovalChanged = payoutApprovalError(http.StatusConflict, "approval_changed",
		"The batch's approval changed since you loaded it; reload and check it again")
)

// ApprovePayoutBatch records actor's approval of the stage the caller saw
// (expectedState). It returns whether the batch is now fully approved.
func ApprovePayoutBatch(b *PayoutBatch, actor, expectedState string, now time.Time) (bool, error) {
	a := &b.Approval
	if !a.Open() {
		return false, ErrPayoutApprovalClosed
	}
	if a.State != expectedState {
		return false, ErrPayoutApprovalChanged
	}
	if actor == b.CreatedBy {
		return false, ErrPayoutSelfApproval
	}
	if a.State == PayoutAwaitingSecondApproval {
		if a.FirstApprovedBy != nil && *a.FirstApprovedBy == actor {
			return false, ErrPayoutSameApprover
		}
		a.SecondApprovedBy, a.SecondApprovedAt, a.State = &actor, &now, PayoutApproved
		return true, nil
	}
	a.FirstApprovedBy, a.FirstApprovedAt = &actor, &now
	if a.RequiredApprovals >= 2 {
		a.State = PayoutAwaitingSecondApproval
		return false, nil
	}
	a.State = PayoutApproved
	return true, nil
}

// ClosePayoutBatch rejects (another admin) or withdraws (its creator) a
// batch still waiting for approval; its items are then cancelled.
func ClosePayoutBatch(b *PayoutBatch, actor, reason string, withdraw bool, now time.Time) error {
	a := &b.Approval
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return apperror.Validation("A reason of 1-500 characters is required")
	}
	if !a.Open() {
		return ErrPayoutApprovalClosed
	}
	if withdraw {
		if actor != b.CreatedBy {
			return apperror.Forbidden("Only the admin who created the batch can withdraw it")
		}
		a.State = PayoutWithdrawn
	} else {
		if actor == b.CreatedBy {
			return apperror.Conflict("The creator withdraws a batch instead of rejecting it")
		}
		a.State = PayoutRejected
	}
	a.DecidedBy, a.DecisionReason, a.DecidedAt = &actor, &reason, &now
	return nil
}
