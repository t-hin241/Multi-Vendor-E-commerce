package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AF-08: shop work notices. A producer states that a shop has work (a paid
// order to prepare, a cancellation or return to handle, a payout result);
// Notification resolves who in the shop is told and sends one notice per
// person. Notices are transactional: there is no SMS, push or marketing.

// Shop notice purposes; each matches the shop permission a staff member
// needs (Vendor decides who holds it) and the category staff opt into.
const (
	PurposeOrders  = "orders"
	PurposeReturns = "returns"
	PurposeFinance = "finance"
)

// VendorCategories are the optional categories a staff member may opt
// into. The owner always receives every category.
var VendorCategories = []string{PurposeOrders, PurposeReturns, PurposeFinance}

// Shop notice templates.
const (
	TypeVendorNewOrder              Type = "vendor_new_order"
	TypeVendorCancellationRequested Type = "vendor_cancellation_requested"
	TypeVendorReturnRequested       Type = "vendor_return_requested"
	TypeVendorReturnDispatched      Type = "vendor_return_dispatched"
	TypeVendorPayoutSucceeded       Type = "vendor_payout_succeeded"
	TypeVendorPayoutFailed          Type = "vendor_payout_failed"
)

type vendorActionKind struct {
	purpose string
	notice  Type
}

// vendorActionKinds is the allowlist of (producer, action kind): a kind
// another producer sends is refused, so Payment cannot announce an order
// and Order cannot announce a payout.
var vendorActionKinds = map[string]vendorActionKind{
	"order:new_order":              {PurposeOrders, TypeVendorNewOrder},
	"order:cancellation_requested": {PurposeOrders, TypeVendorCancellationRequested},
	"order:return_requested":       {PurposeReturns, TypeVendorReturnRequested},
	"order:return_dispatched":      {PurposeReturns, TypeVendorReturnDispatched},
	"payment:payout_succeeded":     {PurposeFinance, TypeVendorPayoutSucceeded},
	"payment:payout_failed":        {PurposeFinance, TypeVendorPayoutFailed},
}

type VendorActionStatus string

const (
	// VendorActionPending waits for its recipients to be resolved;
	// VendorActionResolving is held by a worker (with a lease).
	VendorActionPending   VendorActionStatus = "pending"
	VendorActionResolving VendorActionStatus = "resolving"
	// VendorActionResolved: the notices are written (one per recipient).
	VendorActionResolved VendorActionStatus = "resolved"
	// VendorActionNoRecipient: nobody may receive it (owner locked, shop
	// gone); an admin follows it up and may retry.
	VendorActionNoRecipient VendorActionStatus = "no_recipient"
	// VendorActionParked: Vendor did not answer through every attempt.
	VendorActionParked VendorActionStatus = "parked"
)

// MaxVendorActionAttempts bounds automatic resolution attempts (about an
// hour and a half with Backoff).
const MaxVendorActionAttempts = 8

// VendorAction is one producer event about a shop's work.
type VendorAction struct {
	ID                string
	Source            string
	EventID           string
	VendorID          string
	ActionKind        string
	Purpose           string
	ReferenceID       string
	VendorOrderID     *string
	CorrelationID     *string
	Status            VendorActionStatus
	Attempts          int
	NextAttemptAt     time.Time
	LastError         *string
	RecipientUserIDs  []string
	PermissionVersion *string
	ResolvedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// VendorActionRequest is what a producer reports.
type VendorActionRequest struct {
	Source        string
	EventID       string
	VendorID      string
	ActionKind    string
	ReferenceID   string
	VendorOrderID string
	CorrelationID string
}

// NewVendorAction validates a producer's report against the allowlist and
// builds the pending event.
func NewVendorAction(r VendorActionRequest, now time.Time) (*VendorAction, error) {
	kind, ok := vendorActionKinds[r.Source+":"+r.ActionKind]
	if !ok {
		return nil, fmt.Errorf("%s may not report action %q", r.Source, r.ActionKind)
	}
	if !eventIDPattern.MatchString(r.EventID) {
		return nil, fmt.Errorf("event_id must be 1-100 letters, digits or ._:-")
	}
	if _, err := uuid.Parse(r.VendorID); err != nil {
		return nil, fmt.Errorf("vendor_id must be a shop id")
	}
	if !referencePattern.MatchString(r.ReferenceID) {
		return nil, fmt.Errorf("reference_id must be 1-100 letters, digits or ._:-")
	}
	a := &VendorAction{Source: r.Source, EventID: r.EventID, VendorID: r.VendorID, ActionKind: r.ActionKind, Purpose: kind.purpose,
		ReferenceID: r.ReferenceID, Status: VendorActionPending, NextAttemptAt: now, RecipientUserIDs: []string{}}
	if r.VendorOrderID != "" {
		if _, err := uuid.Parse(r.VendorOrderID); err != nil {
			return nil, fmt.Errorf("vendor_order_id must be a vendor order id")
		}
		a.VendorOrderID = &r.VendorOrderID
	}
	if c := strings.TrimSpace(r.CorrelationID); c != "" && len(c) <= 64 {
		a.CorrelationID = &c
	}
	return a, nil
}

// Notice is the template the action's notices use.
func (a *VendorAction) Notice() Type {
	return vendorActionKinds[a.Source+":"+a.ActionKind].notice
}

// NeedsReview reports whether an admin has to follow the action up (and
// may retry it).
func (a *VendorAction) NeedsReview() bool {
	return a.Status == VendorActionNoRecipient || a.Status == VendorActionParked
}

// Candidate is one person Vendor says may receive a purpose's notices.
type Candidate struct {
	UserID string
	Role   string // owner | staff
}

// SelectRecipients decides who is told: the owner always (work notices
// are required for the owner), staff only for categories they opted into.
// A person listed twice (several roles) is told once. The result is
// sorted, so the same inputs give the same list.
func SelectRecipients(purpose string, candidates []Candidate, optIn map[string][]string) []string {
	out := []string{}
	for _, c := range candidates {
		if slices.Contains(out, c.UserID) {
			continue
		}
		if c.Role == "owner" || (c.Role == "staff" && slices.Contains(optIn[c.UserID], purpose)) {
			out = append(out, c.UserID)
		}
	}
	slices.Sort(out)
	return out
}

// NormalizeVendorCategories validates a person's opt-in list: known
// categories only, no duplicates, sorted.
func NormalizeVendorCategories(in []string) ([]string, error) {
	if len(in) > len(VendorCategories) {
		return nil, fmt.Errorf("too many categories")
	}
	out := []string{}
	for _, c := range in {
		c = strings.TrimSpace(c)
		if !slices.Contains(VendorCategories, c) {
			return nil, fmt.Errorf("unknown category %q; use orders, returns or finance", c)
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out, nil
}
