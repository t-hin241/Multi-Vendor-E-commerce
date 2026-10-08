package domain

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
)

// SettlementHold keeps a vendor order's credits out of payouts while a
// source in Order (a support case, ...) may still change what the vendor
// is owed (00 §6.1). Payment owns the ledger; only the source releases.
type SettlementHold struct {
	ID            string
	VendorID      *string
	VendorOrderID *string
	SourceType    *string
	SourceID      *string
	SourceVersion *int64
	ReasonCode    *string
	Status        HoldStatus
	// PayoutClaimed: a payout batch had already claimed this vendor order's
	// credits when the hold was acquired; the source must review it.
	PayoutClaimed        bool
	AcquiredAt           *time.Time
	ReleaseOperationID   *string
	ReleaseSourceVersion *int64
	ResolutionRef        *string
	ReleaseReason        *string
	ReleasedAt           *time.Time
	CreatedAt            time.Time
}

type HoldStatus string

const (
	HoldActive   HoldStatus = "active"
	HoldReleased HoldStatus = "released"
)

// HoldRequest is a source's acquire command; HoldID is chosen by the
// source and is the idempotency key.
type HoldRequest struct {
	HoldID        string
	VendorID      string
	VendorOrderID string
	SourceType    string
	SourceID      string
	SourceVersion int64
	ReasonCode    string
}

var holdCode = regexp.MustCompile(`^[a-z_]{2,60}$`)

func ValidateHoldRequest(r HoldRequest) error {
	for _, id := range []string{r.HoldID, r.VendorID, r.VendorOrderID, r.SourceID} {
		if _, err := uuid.Parse(id); err != nil {
			return apperror.Validation("hold_id, vendor_id, vendor_order_id and source_id must be UUIDs")
		}
	}
	if !holdCode.MatchString(r.SourceType) || len(r.SourceType) > 40 || !holdCode.MatchString(r.ReasonCode) {
		return apperror.Validation("source_type and reason_code must be lower-case words")
	}
	if r.SourceVersion < 0 {
		return apperror.Validation("source_version must not be negative")
	}
	return nil
}

// Same reports whether a replayed acquire names this hold's source.
func (h *SettlementHold) Same(r HoldRequest) bool {
	eq := func(p *string, v string) bool { return p != nil && *p == v }
	return eq(h.VendorID, r.VendorID) && eq(h.VendorOrderID, r.VendorOrderID) && eq(h.SourceType, r.SourceType) &&
		eq(h.SourceID, r.SourceID) && eq(h.ReasonCode, r.ReasonCode)
}

// HoldRelease is the source's release command; OperationID makes a retry
// return the same receipt.
type HoldRelease struct {
	OperationID   string
	SourceVersion int64
	ResolutionRef string
	Reason        string
}

func ValidateHoldRelease(r HoldRelease) (HoldRelease, error) {
	r.OperationID, r.ResolutionRef, r.Reason = strings.TrimSpace(r.OperationID), strings.TrimSpace(r.ResolutionRef), strings.TrimSpace(r.Reason)
	if r.OperationID == "" || len(r.OperationID) > 100 {
		return r, apperror.Validation("operation_id of 1-100 characters is required")
	}
	if r.Reason == "" || len(r.Reason) > 500 || len(r.ResolutionRef) > 100 {
		return r, apperror.Validation("a reason of 1-500 characters is required; resolution_ref is at most 100")
	}
	return r, nil
}

// Hold API refusals.
var (
	ErrHoldConflict = &apperror.Error{Code: "hold_conflict", Status: http.StatusConflict,
		Message: "This hold id or source already names a different hold"}
	ErrPayoutAlreadyClaimed = &apperror.Error{Code: "payout_already_claimed", Status: http.StatusConflict,
		Message: "A payout already claimed this vendor order's credits; the hold is recorded but the money may have left"}
)
