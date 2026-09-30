package domain

import (
	"errors"
	"net/http"

	"shopee/backend/pkg/apperror"
)

var errMissingQuote = errors.New("a vendor order has no shipping quote")

// Error codes clients can act on, beyond the generic apperror codes.
const (
	CodeCartChanged          apperror.Code = "cart_changed"
	CodeShippingUnavailable  apperror.Code = "shipping_unavailable"
	CodeCheckoutTotalChanged apperror.Code = "checkout_total_changed"
	CodeCheckoutInProgress   apperror.Code = "checkout_in_progress"
	CodeIdempotencyKeyReused apperror.Code = "idempotency_key_reused"
	CodeOrderNotReady        apperror.Code = "order_not_ready"
)

func coded(code apperror.Code, status int, message string) *apperror.Error {
	return &apperror.Error{Code: code, Status: status, Message: message}
}

// CartChanged: the cart differs from what the buyer reviewed.
func CartChanged(message string) *apperror.Error {
	return coded(CodeCartChanged, http.StatusConflict, message)
}

// ShippingUnavailable: no fee could be quoted; the order is not created.
func ShippingUnavailable(message string) *apperror.Error {
	return coded(CodeShippingUnavailable, http.StatusConflict, message)
}

// CheckoutTotalChanged: the total differs from the one the buyer confirmed.
func CheckoutTotalChanged() *apperror.Error {
	return coded(CodeCheckoutTotalChanged, http.StatusConflict,
		"The order total changed since you reviewed it (price or shipping fee). Please review and confirm again.")
}

// CheckoutInProgress: the same checkout request is still being processed.
func CheckoutInProgress() *apperror.Error {
	return coded(CodeCheckoutInProgress, http.StatusConflict,
		"This order is still being placed. Please wait a moment and check your orders.")
}

// IdempotencyKeyReused: the key was used for a different checkout request.
func IdempotencyKeyReused() *apperror.Error {
	return coded(CodeIdempotencyKeyReused, http.StatusConflict,
		"This checkout key was already used for a different request. Please review your order and try again.")
}

// OrderNotReady: the order is still being prepared and cannot be paid yet.
func OrderNotReady() *apperror.Error {
	return coded(CodeOrderNotReady, http.StatusConflict, "This order is still being prepared and cannot be paid yet")
}
