package events

import "shopee/backend/pkg/eventbus"

// AF-08: work a shop has to do. The producer states the fact about a shop;
// Notification decides who in the shop is told (Vendor's recipient list at
// that moment) and sends one notice per person and channel. The payloads
// carry references only: no buyer address, amount or bank account.
const (
	OrderVendorActionRequired   = "order.vendor_action_required"
	PaymentVendorActionRequired = "payment.vendor_action_required"
)

// Kinds of order work (Order → Notification).
const (
	// VendorActionNewOrder: a vendor order is paid and its stock committed;
	// the shop prepares it.
	VendorActionNewOrder = "new_order"
	// VendorActionCancellationRequested: the buyer asked to cancel a paid
	// package (AF-03); the shop must not hand it over meanwhile.
	VendorActionCancellationRequested = "cancellation_requested"
	// VendorActionReturnRequested: the buyer asked to return items.
	VendorActionReturnRequested = "return_requested"
	// VendorActionReturnDispatched: the buyer sent a return parcel to the
	// shop (AF-05); the shop records the goods receipt on arrival.
	VendorActionReturnDispatched = "return_dispatched"
	// PW-009: a buyer opened a support case on the shop's package, or the
	// marketplace is waiting for the shop's answer on one (ReferenceID is
	// the case id).
	VendorActionSupportCaseOpened  = "support_case_opened"
	VendorActionSupportWaitingShop = "support_waiting_shop"
	// PW-009 (AF-04): a failed delivery came back to the shop, which must
	// record the goods receipt; the buyer accepted a redelivery, which the
	// shop prepares (ReferenceID is the delivery exception id).
	VendorActionDeliveryGoodsReturned = "delivery_goods_returned"
	VendorActionRedeliveryAccepted    = "redelivery_accepted"
)

// Payout outcomes (Payment → Notification).
const (
	PayoutSucceeded = "succeeded"
	PayoutFailed    = "failed"
)

// VendorOrderAction: a shop has work on one of its vendor orders. The event
// id is the producer's effect id, so a retry is the same event.
type VendorOrderAction struct {
	VendorID      string `json:"vendor_id"`
	VendorOrderID string `json:"vendor_order_id"`
	OrderID       string `json:"order_id"`
	ActionKind    string `json:"action_kind"`
	// ReferenceID is the request the work is about (cancellation or return
	// id); the vendor order id for a new order.
	ReferenceID string `json:"reference_id"`
}

func VendorOrderActionEvent(eventID string, a VendorOrderAction) (eventbus.Envelope, error) {
	return eventbus.New(eventID, OrderVendorActionRequired, V1, a.VendorOrderID, 0, a)
}

// VendorPayoutAction: a payout transfer to a shop was recorded as
// succeeded or failed. Amount and destination stay in Payment.
type VendorPayoutAction struct {
	VendorID string `json:"vendor_id"`
	PayoutID string `json:"payout_id"`
	Outcome  string `json:"outcome"`
}

func VendorPayoutActionEvent(eventID string, a VendorPayoutAction) (eventbus.Envelope, error) {
	return eventbus.New(eventID, PaymentVendorActionRequired, V1, a.PayoutID, 0, a)
}
