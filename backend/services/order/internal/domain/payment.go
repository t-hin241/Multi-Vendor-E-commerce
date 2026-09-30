package domain

import "time"

// PaymentCapture is Payment's report that money was captured for an order.
// Order checks it against its own snapshot before marking anything paid.
type PaymentCapture struct {
	PaymentID string
	Amount    int64
	Currency  string
}

// OrderPayment is Order's record of a capture it received.
type OrderPayment struct {
	PaymentID       string
	OrderID         string
	Amount          int64
	Currency        string
	Outcome         string
	RejectionReason *string
	ReceivedAt      time.Time
}

const (
	PaymentApplied  = "applied"
	PaymentRejected = "rejected"

	// Rejection reasons: money was taken but cannot pay for the order, so
	// it must be refunded or reviewed.
	RejectAmountMismatch   = "amount_mismatch"
	RejectOrderCancelled   = "order_cancelled"
	RejectDuplicatePayment = "duplicate_payment"
	RejectOrderNotReady    = "order_not_ready"
	RejectStockNotHeld     = "stock_not_held"
)

// JudgeCapture decides whether a new capture can pay for order. It returns
// "" when the capture applies, otherwise the rejection reason. alreadyPaid
// means another capture was already applied to the order.
func JudgeCapture(order *Order, capture PaymentCapture, alreadyPaid bool) string {
	switch {
	case order.Status == StatusCancelled:
		return RejectOrderCancelled
	case alreadyPaid || order.Status.PaidOrFurther():
		return RejectDuplicatePayment
	case order.CheckoutState != CheckoutReady:
		return RejectOrderNotReady
	case capture.Amount != order.TotalAmount || capture.Currency != order.Currency:
		return RejectAmountMismatch
	}
	return ""
}
