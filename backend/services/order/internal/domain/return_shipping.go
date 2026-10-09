package domain

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// AF-05: an approved return's way back. Order authorizes the shipping (the
// shop's verified destination, who pays, a dispatch deadline), the buyer
// reports the carrier and tracking number, Shipment carries the parcel and
// the shop records what arrived. The refund follows the inspection, never
// the parcel's status alone.

// Shipping statuses of a return (Order's view; Shipment keeps the parcel).
const (
	// ShippingDestinationMissing: approved, but the shop has no verified
	// return destination; an admin authorizes once it has.
	ShippingDestinationMissing = "destination_missing"
	ShippingAwaitingDispatch   = "awaiting_dispatch"
	// ShippingAwaitingVerification: the buyer reported the parcel sent;
	// nobody has seen it arrive yet.
	ShippingAwaitingVerification = "awaiting_verification"
	ShippingReceived             = "received"
	// ShippingLost: the parcel did not reach the shop (checked with the
	// carrier by an admin).
	ShippingLost = "lost"
)

// Who pays the return shipping.
const (
	FeePayerBuyer  = "buyer"
	FeePayerSeller = "seller"
)

// DefaultReturnDispatchDays: the buyer sends the parcel within this many
// days of the authorization (AF-02 §4 default).
const DefaultReturnDispatchDays = 7

// ReturnDestination is the shop's verified return address snapshotted on
// a return. A later change at the shop never moves a parcel in transit.
type ReturnDestination struct {
	RecipientName      string `json:"recipient_name"`
	Phone              string `json:"phone"`
	Province           string `json:"province"`
	District           string `json:"district"`
	Ward               string `json:"ward"`
	StreetAddress      string `json:"street_address"`
	ReceivingHours     string `json:"receiving_hours"`
	VendorAddressID    string `json:"vendor_address_id"`
	DestinationVersion int64  `json:"destination_version"`
}

// ReturnCode is what the buyer writes on the parcel.
func ReturnCode(returnID string) string {
	code := strings.ToUpper(strings.ReplaceAll(returnID, "-", ""))
	if len(code) > 10 {
		code = code[:10]
	}
	return "RT-" + code
}

// Authorized: the return has a shipping authorization.
func (r *ReturnRequest) Authorized() bool { return r.AuthorizationVersion > 0 && r.Destination != nil }

// Dispatched: the buyer reported the parcel sent (or it already arrived).
func (r *ReturnRequest) Dispatched() bool {
	return r.ShippingStatus != nil && (*r.ShippingStatus == ShippingAwaitingVerification || *r.ShippingStatus == ShippingReceived ||
		*r.ShippingStatus == ShippingLost)
}

var carrierTrackingPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

// ValidateDispatch checks what the buyer reports: a carrier, a tracking
// number, and a time neither in the future nor before the return existed.
func ValidateDispatch(carrier, tracking string, at, now, created time.Time) (string, string, error) {
	carrier, tracking = strings.TrimSpace(carrier), strings.TrimSpace(tracking)
	if carrier == "" || len(carrier) > 60 {
		return "", "", apperror.Validation("carrier_name must be 1-60 characters")
	}
	if !carrierTrackingPattern.MatchString(tracking) {
		return "", "", coded(CodeInvalidTracking, http.StatusUnprocessableEntity, "tracking_number must be 3-64 letters, digits, '.', '_' or '-'")
	}
	if at.After(now.Add(10*time.Minute)) || at.Before(created.Add(-time.Hour)) {
		return "", "", apperror.Validation("dispatched_at must be after the return was requested and not in the future")
	}
	return carrier, tracking, nil
}

// ReturnReceipt is what the shop received for a return: every unit is
// sellable, damaged or missing.
type ReturnReceipt struct {
	ID         string
	ReturnID   string
	Version    int
	RecordedBy string
	ActorRole  string
	Sellable   int64
	Damaged    int64
	Missing    int64
	Note       *string
	CreatedAt  time.Time
}

// Disputed: something is damaged or missing; the refund waits for an
// admin instead of a silent deduction (AF-01 handles the dispute).
func (r *ReturnReceipt) Disputed() bool { return r.Damaged > 0 || r.Missing > 0 }

// ValidateReturnReceipt: the units add up to the approved quantity, never
// more.
func ValidateReturnReceipt(sellable, damaged, missing, approved int64) error {
	if sellable < 0 || damaged < 0 || missing < 0 {
		return apperror.Validation("Quantities cannot be negative")
	}
	if sellable+damaged+missing != approved {
		return apperror.Validation("Record all " + strconv.FormatInt(approved, 10) + " returned units as sellable, damaged or missing")
	}
	return nil
}

const (
	CodeReturnNotApproved  apperror.Code = "return_not_approved"
	CodeDestinationChanged apperror.Code = "destination_changed"
	CodeInvalidTracking    apperror.Code = "invalid_tracking"
	CodeNoReturnDest       apperror.Code = "return_destination_missing"
	CodeReturnShippingOff  apperror.Code = "return_shipping_disabled"
)

func ReturnNotApproved() *apperror.Error {
	return coded(CodeReturnNotApproved, http.StatusConflict, "This return has no shipping instructions yet")
}

// DestinationChanged: the parcel already left; its destination stays.
func DestinationChanged() *apperror.Error {
	return coded(CodeDestinationChanged, http.StatusConflict, "The parcel was already sent; the return destination can no longer change")
}

func NoReturnDestination() *apperror.Error {
	return coded(CodeNoReturnDest, http.StatusConflict, "The shop has no verified return destination yet")
}

func ReturnShippingDisabled() *apperror.Error {
	return coded(CodeReturnShippingOff, http.StatusConflict, "Return shipping is not enabled")
}

// ReturnChanged: the return moved since the caller read it.
func ReturnChanged() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This return changed since you loaded it; reload and try again")
}
