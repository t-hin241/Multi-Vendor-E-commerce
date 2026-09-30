// Package domain holds Cart's entities and business rules. A cart is only
// the buyer's intent to buy: it points at a product (or variant) and a
// quantity. The price it remembers (SeenPrice*) is a display reference used
// to warn the buyer that the live price moved; it is never a final price —
// Order re-prices every line from Catalog at checkout and snapshots that.
package domain

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"shopee/backend/pkg/apperror"
)

const (
	// MaxLinesPerCart bounds how many distinct product/variant lines a cart
	// holds, which in turn bounds every Catalog/Inventory lookup a cart view
	// or checkout snapshot fans out to.
	MaxLinesPerCart = 50
	// MaxQuantityPerLine is the most units of one product/variant a buyer
	// can keep in a single line.
	MaxQuantityPerLine = 999

	// CodeCartChanged tells the client its view of the cart is stale (a
	// different tab/device, or a checkout that just consumed lines, changed
	// it) and must be reloaded before the buyer confirms again.
	CodeCartChanged apperror.Code = "cart_changed"
)

type Cart struct {
	ID        string
	UserID    string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CartItem struct {
	ID              string
	CartID          string
	ProductID       string
	VariantID       *string
	Quantity        int64
	Version         int64
	SeenPriceAmount *int64
	SeenCurrency    *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SameLine reports whether the item is the line for productID/variantID. A
// product-level line (no variant) and a variant line are different lines
// even for the same product.
func (i *CartItem) SameLine(productID string, variantID *string) bool {
	if variantID == nil {
		return i.VariantID == nil && i.ProductID == productID
	}
	return i.VariantID != nil && *i.VariantID == *variantID
}

// ValidateQuantity checks a quantity being added to or set on a line.
func ValidateQuantity(quantity int64) error {
	if quantity <= 0 {
		return apperror.Validation("Quantity must be a positive number")
	}
	if quantity > MaxQuantityPerLine {
		return apperror.Validation("Quantity cannot exceed " + strconv.Itoa(MaxQuantityPerLine) + " per item")
	}
	return nil
}

// ValidateSetQuantity is ValidateQuantity for an absolute update, where 0
// means "remove the line".
func ValidateSetQuantity(quantity int64) error {
	if quantity == 0 {
		return nil
	}
	if quantity < 0 {
		return apperror.Validation("Quantity cannot be negative")
	}
	return ValidateQuantity(quantity)
}

// AccumulatedQuantity adds delta to an existing line quantity, rejecting a
// result over the per-line limit instead of silently capping it.
func AccumulatedQuantity(current, delta int64) (int64, error) {
	if err := ValidateQuantity(delta); err != nil {
		return 0, err
	}
	if current > MaxQuantityPerLine-delta {
		return 0, apperror.Validation("This item already has " + strconv.FormatInt(current, 10) +
			" in your cart; the limit is " + strconv.Itoa(MaxQuantityPerLine) + " per item")
	}
	return current + delta, nil
}

// EnsureLineCapacity rejects adding a brand-new line to a full cart.
func EnsureLineCapacity(existingLines int) error {
	if existingLines >= MaxLinesPerCart {
		return apperror.Validation("Your cart can hold at most " + strconv.Itoa(MaxLinesPerCart) + " different items; please remove some first")
	}
	return nil
}

// CartChanged is the conflict returned when a conditional mutation or
// snapshot was based on a cart version that is no longer current.
func CartChanged(message string) *apperror.Error {
	if message == "" {
		message = "Your cart changed in another tab or device. Please review it again."
	}
	return &apperror.Error{Code: CodeCartChanged, Message: message, Status: http.StatusConflict}
}

// CheckExpectedVersion implements optimistic concurrency for a cart: a nil
// expectation means the caller opted out (legacy clients during cutover).
func CheckExpectedVersion(expected *int64, actual int64) error {
	if expected == nil {
		return nil
	}
	if *expected != actual {
		return CartChanged("")
	}
	return nil
}

// LineSubtotal multiplies a unit price by quantity in integer minor units,
// reporting overflow instead of wrapping.
func LineSubtotal(priceAmount, quantity int64) (int64, bool) {
	if priceAmount < 0 || quantity < 0 {
		return 0, false
	}
	if quantity != 0 && priceAmount > math.MaxInt64/quantity {
		return 0, false
	}
	return priceAmount * quantity, true
}

// AddAmounts sums two non-negative minor-unit amounts, reporting overflow.
func AddAmounts(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
