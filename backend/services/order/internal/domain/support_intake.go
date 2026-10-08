package domain

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// SupportIntake is a buyer's request without an order id (PW-012): the
// reference they have (checkout, payment or bank transfer) and what
// happened. An admin links it to an order verified to be the buyer's,
// which opens or extends a support case.
type SupportIntake struct {
	ID             string
	BuyerID        string
	ReferenceKind  string
	Reference      string
	Message        string
	Status         string
	LinkedCaseID   *string
	HandledBy      *string
	HandledAt      *time.Time
	CloseReason    *string
	IdempotencyKey *string
	RequestHash    *string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const (
	IntakeOpen   = "open"
	IntakeLinked = "linked"
	IntakeClosed = "closed"

	// MaxOpenIntakes bounds what one buyer can leave waiting.
	MaxOpenIntakes = 3

	AuditSupportIntake = "support_intake"
)

var intakeReference = regexp.MustCompile(`^[A-Za-z0-9 ._:/#-]{3,100}$`)

// ValidateIntakeReference checks the kind and trims the reference.
func ValidateIntakeReference(kind, reference string) (string, error) {
	switch kind {
	case "checkout", "payment", "bank_transfer":
	default:
		return "", apperror.Validation("reference_kind must be checkout, payment or bank_transfer")
	}
	reference = strings.Join(strings.Fields(reference), " ")
	if !intakeReference.MatchString(reference) {
		return "", apperror.Validation("reference must be 3-100 letters, digits, spaces or . _ : / # -")
	}
	return reference, nil
}

const (
	CodeIntakeOrderMismatch apperror.Code = "intake_order_mismatch"
	CodeTooManyIntakes      apperror.Code = "too_many_open_intakes"
	CodeIntakeAlreadyOpen   apperror.Code = "intake_already_open"
)

// IntakeOrderMismatch: the order an admin picked is not the buyer's.
func IntakeOrderMismatch() *apperror.Error {
	return coded(CodeIntakeOrderMismatch, http.StatusUnprocessableEntity, "This order does not belong to the buyer who sent the request")
}

func TooManyIntakes() *apperror.Error {
	return coded(CodeTooManyIntakes, http.StatusConflict, "You already have 3 requests waiting for the marketplace; we will answer them first")
}

func IntakeAlreadyOpen() *apperror.Error {
	return coded(CodeIntakeAlreadyOpen, http.StatusConflict, "You already sent a request with this reference; it is waiting for the marketplace")
}

// IntakeChanged: the intake was handled since the admin loaded it.
func IntakeChanged() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This request changed since you loaded it; reload and try again")
}
