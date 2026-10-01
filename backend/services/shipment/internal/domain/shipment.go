// Package domain holds Shipment's entities and business rules: the
// fulfillment state machine, tracking input rules, what Order is told, and
// how long a buyer's address stays readable.
package domain

import (
	"regexp"
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
	// StatusReturned: the carrier brought the package back to the vendor
	// (delivery failed for good). Money is Order's decision.
	StatusReturned Status = "returned"
)

// validTransitions is forward-only. Before handover a shipment can be
// cancelled directly; after handover only the carrier can stop it
// (interception), and a package either reaches the buyer (delivered) or
// comes back (returned). Delivered, cancelled and returned are final.
var validTransitions = map[Status][]Status{
	StatusPending:               {StatusReadyToShip, StatusCancelled},
	StatusReadyToShip:           {StatusShipped, StatusCancelled},
	StatusShipped:               {StatusDelivered, StatusInterceptionRequested, StatusReturned},
	StatusInterceptionRequested: {StatusShipped, StatusCancelled, StatusReturned},
	StatusDelivered:             {},
	StatusCancelled:             {},
	StatusReturned:              {},
}

func CanTransition(from, to Status) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Final statuses never change again.
func (s Status) Final() bool {
	return s == StatusDelivered || s == StatusCancelled || s == StatusReturned
}

var cancellableStatuses = map[Status]bool{
	StatusPending:     true,
	StatusReadyToShip: true,
}

// IsCancellable: nothing has left the warehouse yet.
func IsCancellable(status Status) bool {
	return cancellableStatuses[status]
}

// Shipment is one vendor order's package. Carrier, fee and destination are
// snapshots taken at creation; only status, tracking and delivery facts
// change afterwards, each with a version bump (compare-and-set).
type Shipment struct {
	ID                   string
	VendorOrderID        string
	VendorID             string
	BuyerID              string
	Status               Status
	Version              int64
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
	ReturnedAt           *time.Time
	CancelledAt          *time.Time
	TrackingUpdatedAt    *time.Time
	FailedAttempts       int
	LastAttemptReason    *string
	InterceptProviderRef *string
	InterceptRequestedAt *time.Time
	InterceptResolvedAt  *time.Time
	AddressRedactedAt    *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

var trackingPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

// NormalizeTrackingNumber is required before a package can be marked
// shipped: a buyer cannot follow a package without one.
func NormalizeTrackingNumber(trackingNumber string) (string, error) {
	t := strings.TrimSpace(trackingNumber)
	if t == "" {
		return "", apperror.Validation("Tracking number is required to mark a shipment as shipped")
	}
	if !trackingPattern.MatchString(t) {
		return "", apperror.Validation("Tracking number must be 3-64 letters, digits, '.', '_' or '-'")
	}
	return t, nil
}

// ValidateTrackingNumber is kept for callers that only check.
func ValidateTrackingNumber(trackingNumber string) error {
	_, err := NormalizeTrackingNumber(trackingNumber)
	return err
}

// ValidateNote bounds a free-text reason or note.
func ValidateNote(note string, required bool, field string) (*string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		if required {
			return nil, apperror.Validation(field + " is required")
		}
		return nil, nil
	}
	if len(note) > 500 {
		return nil, apperror.Validation(field + " must be at most 500 characters")
	}
	return &note, nil
}

// OrderEvent is a fulfillment fact Order is told about. Order decides what
// it means for the vendor order (shipped, completed); Shipment never writes
// Order's data.
type OrderEvent string

const (
	OrderEventShipped   OrderEvent = "shipped"
	OrderEventDelivered OrderEvent = "delivered"
	OrderEventReturned  OrderEvent = "returned"
)

// OrderEventFor names the event a status change produces, if any.
func OrderEventFor(to Status) (OrderEvent, bool) {
	switch to {
	case StatusShipped:
		return OrderEventShipped, true
	case StatusDelivered:
		return OrderEventDelivered, true
	case StatusReturned:
		return OrderEventReturned, true
	}
	return "", false
}

// ActorRole is who changed a shipment.
type ActorRole string

const (
	ActorVendor  ActorRole = "vendor"
	ActorAdmin   ActorRole = "admin"
	ActorCarrier ActorRole = "carrier"
	ActorSystem  ActorRole = "system"
)

// AddressReadableFor: a vendor needs the buyer's address and phone only to
// get the package there. Once the shipment is final, the vendor's view
// keeps the province and district only.
func (s *Shipment) AddressReadableFor(role ActorRole) bool {
	if s.AddressRedactedAt != nil {
		return false
	}
	return role != ActorVendor || !s.Status.Final()
}

// ForVendor returns a copy without the buyer's contact details once the
// vendor no longer needs them.
func (s *Shipment) ForVendor() *Shipment {
	cp := *s
	if !cp.AddressReadableFor(ActorVendor) {
		cp.RecipientName, cp.Phone, cp.StreetAddress, cp.Ward = nil, nil, nil, nil
	}
	return &cp
}

// Stale thresholds for the operations view.
const (
	// FulfillmentLag: paid but not handed to the carrier.
	FulfillmentLag = 3 * 24 * time.Hour
	// TrackingStale: in transit with no update.
	TrackingStale = 7 * 24 * time.Hour
	// InterceptionStale: an interception with no decision.
	InterceptionStale = 24 * time.Hour
)
