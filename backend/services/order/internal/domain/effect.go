package domain

import (
	"encoding/json"
	"time"
)

// EffectKind names a side effect of an order transition that Order must
// eventually carry out in another service.
type EffectKind string

const (
	EffectCreateShipment   EffectKind = "create_shipment"
	EffectCancelShipment   EffectKind = "cancel_shipment"
	EffectReleaseInventory EffectKind = "release_inventory"
	EffectNotify           EffectKind = "notify"
	EffectRequestRefund    EffectKind = "request_refund"
	EffectRestockReturn    EffectKind = "restock_return"
	// EffectSettleVendorOrder reports a completed vendor order to Payment's
	// settlement ledger.
	EffectSettleVendorOrder EffectKind = "settle_vendor_order"
	// EffectReportRejectedOutcome tells Payment that Order refused a payment
	// or refund outcome it received as an event, so Payment reviews it.
	EffectReportRejectedOutcome EffectKind = "report_rejected_outcome"
	// AF-03: stop a cancelled vendor order at Shipment, and put its
	// units back at Inventory once per item. Target is the request id.
	EffectStopFulfillment       EffectKind = "stop_fulfillment"
	EffectRecoverCancelledStock EffectKind = "recover_cancelled_stock"
)

// RejectedOutcomePayload is what a report_rejected_outcome effect sends.
type RejectedOutcomePayload struct {
	Kind            string `json:"kind"` // payment | refund
	PaymentID       string `json:"payment_id,omitempty"`
	PaymentRefundID string `json:"payment_refund_id,omitempty"`
	Reason          string `json:"reason"`
}

// Effect is a durable task written in the same transaction as the order
// change that caused it; a worker retries it until the other service
// confirms. (OrderID, Kind, Target) is unique, so enqueueing twice is a
// no-op.
type Effect struct {
	ID            string
	OrderID       string
	Kind          EffectKind
	Target        string
	Payload       json.RawMessage
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	CreatedAt     time.Time
}

const (
	EffectPending = "pending"
	EffectDone    = "done"
	EffectParked  = "parked"

	// MaxEffectAttempts bounds retries (about 9 hours with EffectBackoff).
	MaxEffectAttempts = 25
)

// NotifyPayload is the payload of a notify effect.
type NotifyPayload struct {
	UserID string `json:"user_id"`
	Type   string `json:"type"`
}

// NewNotifyEffect notifies userID once per (order, type).
func NewNotifyEffect(orderID, userID, notifType string) Effect {
	payload, _ := json.Marshal(NotifyPayload{UserID: userID, Type: notifType})
	return Effect{OrderID: orderID, Kind: EffectNotify, Target: notifType, Payload: payload}
}

// EffectBackoff is the delay before retry number attempts: 5s doubling,
// capped at 30 minutes.
func EffectBackoff(attempts int) time.Duration {
	return CartConsumeBackoff(attempts)
}

// EffectStats summarizes the side-effect backlog for monitoring.
type EffectStats struct {
	Pending       int64
	Parked        int64
	OldestPending *time.Time
}
