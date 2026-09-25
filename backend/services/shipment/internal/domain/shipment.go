// Package domain holds Shipment's entities and business rules: the
// fulfillment status state machine and the invariant that a shipment can't
// be marked shipped without a tracking number to show the buyer.
package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type Status string

const (
	StatusPending               Status = "pending"
	StatusReadyToShip           Status = "ready_to_ship"
	StatusShipped               Status = "shipped"
	StatusDelivered             Status = "delivered"
	StatusCancelled             Status = "cancelled"
	StatusInterceptionRequested Status = "interception_requested"
)

// validTransitions is forward-only, with two branch off ramps. A shipment
// can still be voided (cancelled) directly while nothing has physically
// left the warehouse yet (pending/ready_to_ship). Once it's handed to the
// carrier (shipped), it can no longer be cancelled directly — a package
// already in transit can only be pulled back by asking the carrier to
// intercept it, which is a separate, carrier-mediated detour
// (shipped -> interception_requested -> cancelled if the carrier accepts,
// or back to shipped if the carrier can't stop it) driven by
// ShipmentUseCase.CancelForVendorOrder / ProcessCarrierWebhook, never by
// Advance directly.
var validTransitions = map[Status][]Status{
	StatusPending:               {StatusReadyToShip, StatusCancelled},
	StatusReadyToShip:           {StatusShipped, StatusCancelled},
	StatusShipped:               {StatusDelivered, StatusInterceptionRequested},
	StatusInterceptionRequested: {StatusShipped, StatusCancelled},
	StatusDelivered:             {},
	StatusCancelled:             {},
}

func CanTransition(from, to Status) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// CancellableStatuses are the only statuses CancelForVendorOrder cancels
// directly — "shipped" is handled by a separate carrier-interception
// branch (see ShipmentUseCase.CancelForVendorOrder), and everything else
// (delivered/cancelled/interception_requested) is left untouched.
var cancellableStatuses = map[Status]bool{
	StatusPending:     true,
	StatusReadyToShip: true,
}

func IsCancellable(status Status) bool {
	return cancellableStatuses[status]
}

// Shipment. Carrier is fixed at creation time (auto-selected from the
// vendor's default shipping method, its fee already quoted and charged to
// the buyer) — the vendor's own Advance calls never change it, only the
// tracking number. Everything from BuyerID through StreetAddress is a
// snapshot taken once at creation, never re-derived, matching the
// codebase's "snapshot at the boundary" convention used elsewhere for
// order_items.product_name / variant_label.
type Shipment struct {
	ID                   string
	VendorOrderID        string
	VendorID             string
	BuyerID              string
	Status               Status
	CarrierID            *string
	TrackingNumber       *string
	ZoneID               *string
	ZoneName             *string
	FeeRuleID            *string
	FeeAmount            int64
	PackageWeightGrams   *int64
	RecipientName        *string
	Phone                *string
	Province             *string
	District             *string
	Ward                 *string
	StreetAddress        *string
	ShippedAt            *time.Time
	DeliveredAt          *time.Time
	InterceptProviderRef *string
	InterceptRequestedAt *time.Time
	InterceptResolvedAt  *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ValidateTrackingNumber is required before a shipment can move to
// "shipped" — a buyer can't track a package with no tracking number
// attached. Carrier is no longer supplied here: it was already fixed and
// charged for at creation time.
func ValidateTrackingNumber(trackingNumber string) error {
	if strings.TrimSpace(trackingNumber) == "" {
		return apperror.Validation("Tracking number is required to mark a shipment as shipped")
	}
	return nil
}
