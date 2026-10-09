package domain

import "time"

// PW-001: returns and refunds hold the vendor order's payout through
// Payment's ledger like support cases, paid cancellations and delivery
// exceptions. A source has at most one hold.
const (
	HoldSourceReturn = "return"
	HoldSourceRefund = "refund"
)

// SourceHold is the hold of one return or refund.
type SourceHold struct {
	SourceType    string
	SourceID      string
	OrderID       string
	VendorID      string
	VendorOrderID string
	HoldID        string
	Status        string
	Note          *string
	UpdatedAt     time.Time
}

// PaymentSourceType is the source type Payment's ledger records.
func (h *SourceHold) PaymentSourceType() string {
	if h.SourceType == HoldSourceRefund {
		return "order_refund"
	}
	return "return_request"
}

// ReasonCode names why the payout is held.
func (h *SourceHold) ReasonCode() string { return h.SourceType + "_open" }

// Open: the hold still has to be released once the source is final.
func (h *SourceHold) Open() bool {
	return h.Status == HoldPreparing || h.Status == HoldActive || h.Status == HoldNeedsReview
}
