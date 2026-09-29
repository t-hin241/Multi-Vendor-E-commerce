package domain

import "time"

type ReturnRequestStatus string

const (
	ReturnRequested              ReturnRequestStatus = "requested"
	ReturnVendorConfirmed        ReturnRequestStatus = "vendor_confirmed"
	ReturnRejected               ReturnRequestStatus = "rejected"
	ReturnAwaitingProviderRefund ReturnRequestStatus = "approved_awaiting_provider_refund"
	ReturnRefunded               ReturnRequestStatus = "refunded"
)

type ReturnRequest struct {
	ID                string
	OrderID           string
	OrderItemID       string
	BuyerID           string
	Reason            string
	Status            ReturnRequestStatus
	VendorConfirmedBy *string
	VendorConfirmedAt *time.Time
	DecidedBy         *string
	DecisionNote      *string
	DecidedAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
