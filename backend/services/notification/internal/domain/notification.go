// Package domain holds Notification's entities: a record of every
// notification this service attempted to send, kept for admin visibility
// and debugging. Notification never decides order/payment/vendor lifecycle
// — it only reacts to events those services already applied.
package domain

import "time"

type Status string

const (
	StatusSent   Status = "sent"
	StatusFailed Status = "failed"
)

// Type identifies which templated message was sent. Kept as a plain string
// type rather than free text so callers and the (mock) sender agree on a
// fixed vocabulary.
type Type string

const (
	TypeOrderPaid      Type = "order_paid"
	TypeOrderShipped   Type = "order_shipped"
	TypeOrderCompleted Type = "order_completed"
	TypeOrderCancelled Type = "order_cancelled"
	TypeOrderRefunded  Type = "order_refunded"
	TypeVendorApproved Type = "vendor_approved"
	TypeVendorRejected Type = "vendor_rejected"
)

type Notification struct {
	ID          string
	UserID      string
	Type        Type
	ReferenceID string
	Status      Status
	FailReason  *string
	CreatedAt   time.Time
}
