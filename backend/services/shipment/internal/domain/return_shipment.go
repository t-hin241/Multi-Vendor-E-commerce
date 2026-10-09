package domain

import (
	"net/http"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// ReturnShipmentStatus is where a returned parcel is (AF-05).
type ReturnShipmentStatus string

const (
	ReturnPendingDispatch   ReturnShipmentStatus = "pending_dispatch"
	ReturnInTransit         ReturnShipmentStatus = "in_transit"
	ReturnReceived          ReturnShipmentStatus = "received"
	ReturnDeliveryException ReturnShipmentStatus = "delivery_exception"
)

// Active: the parcel can still move.
func (s ReturnShipmentStatus) Active() bool {
	return s == ReturnPendingDispatch || s == ReturnInTransit
}

// ReturnShipment carries returned goods to the shop's return destination.
// Order drives it; the destination is the snapshot Order authorized.
type ReturnShipment struct {
	ID                   string
	ReturnID             string
	OrderID              string
	VendorID             string
	BuyerID              string
	AuthorizationVersion int
	OperationID          string
	Status               ReturnShipmentStatus
	RecipientName        string
	Phone                string
	Province             string
	District             string
	Ward                 string
	StreetAddress        string
	ReceivingHours       string
	CarrierName          *string
	TrackingNumber       *string
	DispatchedAt         *time.Time
	DispatchOperationID  *string
	ReceivedAt           *time.Time
	ExceptionReason      *string
	ExceptionAt          *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ReturnTransitStale: a parcel in transit this long needs a look.
const ReturnTransitStale = 10 * 24 * time.Hour

// ValidateCarrierName bounds the carrier a buyer names.
func ValidateCarrierName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return "", apperror.Validation("carrier_name must be 1-60 characters")
	}
	return name, nil
}

const (
	CodeDestinationChanged apperror.Code = "destination_changed"
	CodeReturnShippingOff  apperror.Code = "return_shipping_disabled"
)

// DestinationChanged: the parcel left with another destination.
func DestinationChanged() *apperror.Error {
	return coded(CodeDestinationChanged, http.StatusConflict, "The parcel was already sent; its destination can no longer change")
}

func ReturnShippingDisabled() *apperror.Error {
	return coded(CodeReturnShippingOff, http.StatusConflict, "Return shipping is not enabled")
}
