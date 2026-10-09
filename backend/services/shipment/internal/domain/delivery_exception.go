package domain

import (
	"net/http"
	"strings"

	"shopee/backend/pkg/apperror"
)

// AF-04: Shipment records why delivery failed for good and tells Order;
// Order owns the resolution (redelivery or refund). Shipment never decides
// money or stock.

// ExceptionType is a delivery failure fact Order is told about.
type ExceptionType string

const (
	// ExceptionAttemptsExhausted: the carrier failed the configured number
	// of attempts; the package may still be with the carrier.
	ExceptionAttemptsExhausted ExceptionType = "attempts_exhausted"
	// ExceptionReturned: the package came back to the shop.
	ExceptionReturned ExceptionType = "returned"
	// ExceptionLost: the carrier confirmed the package lost.
	ExceptionLost ExceptionType = "lost"
)

// OutboxType is the outbox event type of an exception fact.
func (t ExceptionType) OutboxType() OrderEvent { return OrderEvent("exception_" + string(t)) }

// ExceptionTypeOf reads an outbox event type back.
func ExceptionTypeOf(e OrderEvent) (ExceptionType, bool) {
	t, ok := strings.CutPrefix(string(e), "exception_")
	switch ExceptionType(t) {
	case ExceptionAttemptsExhausted, ExceptionReturned, ExceptionLost:
		return ExceptionType(t), ok
	}
	return "", false
}

// ExceptionFor names the exception a final status produces, if any.
func ExceptionFor(to Status) (ExceptionType, bool) {
	switch to {
	case StatusReturned:
		return ExceptionReturned, true
	case StatusLost:
		return ExceptionLost, true
	}
	return "", false
}

// DefaultAttemptLimit: failed attempts before the case goes to an
// operator (AF-04 §4). The customer is never assumed to have refused.
const DefaultAttemptLimit = 2

// MaxAttempts bounds redeliveries of one vendor order (the first attempt
// included).
const MaxAttempts = 3

// FailureKinds a failure report may record, and who may record each: a
// returned package is something the shop holds; a lost one needs the
// carrier's confirmation, checked by an admin.
var FailureKinds = map[string]Status{"returned": StatusReturned, "lost": StatusLost}

// RedeliverableFrom: a replacement attempt follows an attempt that ended
// without reaching the buyer.
func RedeliverableFrom(s Status) bool { return s == StatusReturned || s == StatusLost }

// Active statuses: at most one attempt per vendor order is in one.
func (s Status) Active() bool {
	return s == StatusPending || s == StatusReadyToShip || s == StatusShipped || s == StatusInterceptionRequested
}

const (
	CodeDeliveryResolutionOff apperror.Code = "delivery_resolution_disabled"
	CodeActiveAttemptExists   apperror.Code = "active_attempt_exists"
	CodeVersionConflict       apperror.Code = "version_conflict"
)

func coded(code apperror.Code, status int, message string) *apperror.Error {
	return &apperror.Error{Code: code, Status: status, Message: message}
}

func DeliveryResolutionDisabled() *apperror.Error {
	return coded(CodeDeliveryResolutionOff, http.StatusConflict, "Delivery exception handling is not enabled")
}

func ActiveAttemptExists() *apperror.Error {
	return coded(CodeActiveAttemptExists, http.StatusConflict, "This order already has a delivery attempt in progress")
}

// ShipmentChanged: the shipment moved since the caller read it.
func ShipmentChanged() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This shipment changed since you loaded it; reload and try again")
}
