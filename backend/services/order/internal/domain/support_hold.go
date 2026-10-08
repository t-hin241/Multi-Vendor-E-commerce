package domain

import (
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
)

// Settlement hold of a support case in Payment's ledger (PW-001, 00 §6.1).
const (
	// HoldPreparing: the acquire effect has not been confirmed by Payment;
	// the case does not count as protecting the money yet.
	HoldPreparing = "preparing"
	HoldActive    = "active"
	// HoldNeedsReview: Payment found the vendor order already claimed by a
	// payout (or refused the hold); an operator must recover it.
	HoldNeedsReview = "needs_review"
	HoldReleasing   = "releasing"
	HoldReleased    = "released"
)

// Effects that carry a support case's hold to Payment; Target is the case.
const (
	EffectAcquireSettlementHold EffectKind = "acquire_settlement_hold"
	EffectReleaseSettlementHold EffectKind = "release_settlement_hold"
)

// CaseHold is a support case's hold as Order knows it.
type CaseHold struct {
	CaseID    string
	OrderID   string
	HoldID    string
	Status    string
	Note      *string
	UpdatedAt time.Time
}

// Open: the hold still has to be released when the case closes.
func (h *CaseHold) Open() bool {
	return h.Status == HoldPreparing || h.Status == HoldActive || h.Status == HoldNeedsReview
}

// HoldReasonCode names the case category in Payment's ledger.
func HoldReasonCode(c *SupportCase) string { return "support_case_" + string(c.Category) }

// CodeHoldUnavailable: the case's payout hold is not confirmed yet.
const CodeHoldUnavailable apperror.Code = "hold_unavailable"

// HoldUnavailable: a financial step must wait until Payment confirmed the
// hold (00 §6.1: never report the money as protected before that).
func HoldUnavailable() *apperror.Error {
	return coded(CodeHoldUnavailable, http.StatusServiceUnavailable,
		"The payout hold for this case is not confirmed by Payment yet; try again shortly")
}
