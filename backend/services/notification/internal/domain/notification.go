// Package domain holds Notification's entities: a notification is a
// durable delivery job, recorded before anything is sent, with every
// attempt kept for operators. Notification never decides order, payment or
// vendor lifecycle — it only tells people about events those services
// already applied.
package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Status string

const (
	// StatusPending waits for its next attempt; StatusSending is claimed by
	// a worker (with a lease, so a crashed worker's job is picked up again).
	StatusPending Status = "pending"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	// StatusFailed: a permanent failure (no recipient, address refused);
	// retrying cannot help. StatusParked: transient failures used up every
	// attempt; an admin may retry once the cause is fixed.
	StatusFailed Status = "failed"
	StatusParked Status = "parked"
)

// Type identifies which templated message is sent.
type Type string

const (
	TypeOrderPaid      Type = "order_paid"
	TypeOrderShipped   Type = "order_shipped"
	TypeOrderCompleted Type = "order_completed"
	TypeOrderCancelled Type = "order_cancelled"
	TypeOrderRefunded  Type = "order_refunded"
	TypeVendorApproved Type = "vendor_approved"
	TypeVendorRejected Type = "vendor_rejected"
	// Support cases (Order, AF-01); the reference is the order id.
	TypeSupportCaseOpened   Type = "support_case_opened"
	TypeSupportCaseResolved Type = "support_case_resolved"
	// Paid-order cancellation (Order, AF-03); the reference is the order id.
	TypeCancellationRequested Type = "cancellation_requested"
	TypeCancellationApproved  Type = "cancellation_approved"
	TypeCancellationRejected  Type = "cancellation_rejected"
	// Policies (Vendor, AF-02); the reference is the shop id.
	TypeMarketplacePolicyUpdated Type = "marketplace_policy_updated"
	TypeShopPolicyApproved       Type = "shop_policy_approved"
	TypeShopPolicyRejected       Type = "shop_policy_rejected"
)

// DefaultMaxAttempts bounds transient retries (about four hours of backoff).
const DefaultMaxAttempts = 8

type Notification struct {
	ID              string
	EventID         string
	Source          string
	UserID          string
	Type            Type
	TemplateVersion string
	ReferenceID     string
	CorrelationID   *string
	DedupKey        string
	Status          Status
	RecipientMasked *string
	FailReason      *string
	Attempts        int
	MaxAttempts     int
	NextAttemptAt   time.Time
	SentAt          *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Attempt is one delivery try, kept for operators.
type Attempt struct {
	ID         string
	Attempt    int
	Outcome    string
	Error      *string
	DurationMS int
	CreatedAt  time.Time
}

var (
	eventIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
	sourcePattern    = regexp.MustCompile(`^[a-z]{1,30}$`)
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
)

// Request is what a producing service asks for.
type Request struct {
	EventID       string
	Source        string
	UserID        string
	Type          Type
	ReferenceID   string
	CorrelationID string
}

// NewNotification validates a request and builds the pending notification.
// Without an event id the producer's event is identified by type and
// reference (one notice per type per order or shop), as before.
func NewNotification(r Request, now time.Time) (*Notification, error) {
	if !referencePattern.MatchString(r.ReferenceID) {
		return nil, fmt.Errorf("reference_id must be 1-100 letters, digits or ._:-")
	}
	t, ok := templates[r.Type]
	if !ok {
		return nil, fmt.Errorf("unknown notification type %q", r.Type)
	}
	if r.Source == "" {
		r.Source = "legacy"
	}
	if !sourcePattern.MatchString(r.Source) {
		return nil, fmt.Errorf("source must be lowercase letters")
	}
	if r.EventID == "" {
		r.EventID = string(r.Type) + ":" + r.ReferenceID
	}
	if !eventIDPattern.MatchString(r.EventID) {
		return nil, fmt.Errorf("event_id must be 1-100 letters, digits or ._:-")
	}
	n := &Notification{
		EventID: r.EventID, Source: r.Source, UserID: r.UserID, Type: r.Type, TemplateVersion: t.Version,
		ReferenceID: r.ReferenceID, Status: StatusPending, MaxAttempts: DefaultMaxAttempts, NextAttemptAt: now,
	}
	if c := strings.TrimSpace(r.CorrelationID); c != "" && len(c) <= 64 {
		n.CorrelationID = &c
	}
	n.DedupKey = strings.Join([]string{n.Source, n.EventID, n.UserID, string(n.Type), n.TemplateVersion}, "|")
	return n, nil
}

// Backoff is the delay before the next attempt: 30s doubling, at most an hour.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second
	for i := 1; i < attempt && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

// MaskEmail keeps the first character of the local part and the domain:
// enough to tell recipients apart in the admin view, not the address.
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}

// Retryable says whether an admin may send the notification again.
func (n *Notification) Retryable() bool {
	return n.Status == StatusFailed || n.Status == StatusParked
}
