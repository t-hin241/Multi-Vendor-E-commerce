package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// ApprovalKind is a manual money action that needs a second admin (AF-19).
type ApprovalKind string

const (
	ApprovalRefundResolution     ApprovalKind = "refund_resolution"
	ApprovalPayoutItemResolution ApprovalKind = "payout_item_resolution"
	ApprovalSettlementAdjustment ApprovalKind = "settlement_adjustment"
)

type ApprovalStatus string

const (
	ApprovalDraft     ApprovalStatus = "draft"
	ApprovalPending   ApprovalStatus = "pending"
	ApprovalApproved  ApprovalStatus = "approved"
	ApprovalRejected  ApprovalStatus = "rejected"
	ApprovalExpired   ApprovalStatus = "expired"
	ApprovalCancelled ApprovalStatus = "cancelled"

	// ApprovalTTL is how long a request waits for its checker.
	ApprovalTTL = 24 * time.Hour

	// Reauthentication purposes; the operation reference is the payload hash.
	ProofPurposeSubmit = "payment.approval.submit"
	ProofPurposeDecide = "payment.approval.decide"
)

// ApprovalRequest is one immutable maker-checker request.
type ApprovalRequest struct {
	ID                       string
	Kind                     ApprovalKind
	TargetID                 string
	Payload                  json.RawMessage
	PayloadHash              string
	Snapshot                 json.RawMessage
	Status                   ApprovalStatus
	MakerID                  string
	MakerPermissionVersion   int64
	Reason                   string
	CheckerID                *string
	CheckerPermissionVersion *int64
	DecisionReason           *string
	Version                  int64
	ExpiresAt                time.Time
	CreatedAt                time.Time
	SubmittedAt              *time.Time
	DecidedAt                *time.Time
	ExecutionRef             *string
}

// Open reports whether the request still waits for its maker or checker.
func (r *ApprovalRequest) Open() bool {
	return r.Status == ApprovalDraft || r.Status == ApprovalPending
}

// Expired reports whether an open request ran out of time at now.
func (r *ApprovalRequest) Expired(now time.Time) bool { return r.Open() && !now.Before(r.ExpiresAt) }

// ResolutionPayload is the asked result of a refund or payout transfer.
type ResolutionPayload struct {
	Outcome           string `json:"outcome"`
	EvidenceReference string `json:"evidence_reference,omitempty"`
	Note              string `json:"note,omitempty"`
}

// AdjustmentPayload is an asked manual ledger correction.
type AdjustmentPayload struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Reason   string `json:"reason"`
}

// NormalizeApprovalPayload validates raw for kind and returns its
// canonical JSON (the bytes that are hashed and stored).
func NormalizeApprovalPayload(kind ApprovalKind, raw json.RawMessage) (json.RawMessage, error) {
	switch kind {
	case ApprovalRefundResolution, ApprovalPayoutItemResolution:
		var p ResolutionPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, apperror.Validation("Invalid resolution")
		}
		p.EvidenceReference, p.Note = strings.TrimSpace(p.EvidenceReference), strings.TrimSpace(p.Note)
		switch {
		case p.Outcome == "succeeded" && (p.EvidenceReference == "" || len(p.EvidenceReference) > 200):
			return nil, apperror.Validation("A succeeded result needs the provider or bank reference (max 200 characters)")
		case p.Outcome == "failed" && (p.Note == "" || len(p.Note) > 500):
			return nil, apperror.Validation("A failed result needs the failure reason (max 500 characters)")
		case p.Outcome != "succeeded" && p.Outcome != "failed":
			return nil, apperror.Validation("Outcome must be succeeded or failed")
		}
		return json.Marshal(p)
	case ApprovalSettlementAdjustment:
		var p AdjustmentPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, apperror.Validation("Invalid adjustment")
		}
		p.Currency, p.Reason = strings.ToUpper(strings.TrimSpace(p.Currency)), strings.TrimSpace(p.Reason)
		if err := ValidateAdjustment(p.Amount, p.Currency, p.Reason); err != nil {
			return nil, err
		}
		return json.Marshal(p)
	}
	return nil, apperror.Validation("Unknown operation kind")
}

// ApprovalPayloadHash binds a proof to exactly this operation.
func ApprovalPayloadHash(kind ApprovalKind, targetID string, payload json.RawMessage) string {
	sum := sha256.Sum256([]byte(string(kind) + "\n" + targetID + "\n" + string(payload)))
	return hex.EncodeToString(sum[:])
}

// RefundSnapshot is what the checker approves about a refund.
type RefundSnapshot struct {
	Status   RefundStatus `json:"status"`
	Amount   int64        `json:"amount"`
	Currency string       `json:"currency"`
	OrderID  string       `json:"order_id"`
}

func SnapshotRefund(r *Refund) RefundSnapshot {
	return RefundSnapshot{Status: r.Status, Amount: r.Amount, Currency: r.Currency, OrderID: r.OrderID}
}

// PayoutItemSnapshot is what the checker approves about a transfer: the
// amount and the exact destination version it pays.
type PayoutItemSnapshot struct {
	Status               PayoutItemStatus `json:"status"`
	VendorID             string           `json:"vendor_id"`
	Amount               int64            `json:"amount"`
	Currency             string           `json:"currency"`
	DestinationAccountID string           `json:"destination_account_id"`
	DestinationVersion   int64            `json:"destination_version"`
}

func SnapshotPayoutItem(i *PayoutItem) PayoutItemSnapshot {
	return PayoutItemSnapshot{Status: i.Status, VendorID: i.VendorID, Amount: i.Amount, Currency: i.Currency,
		DestinationAccountID: i.DestinationAccountID, DestinationVersion: i.DestinationVersion}
}
