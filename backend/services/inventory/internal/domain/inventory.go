// Package domain holds Inventory's entities and business rules: stock
// levels, reservation lifecycle, and the invariant that a sale can never
// exceed what's actually on hand.
package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

type ReservationStatus string

const (
	ReservationActive    ReservationStatus = "active"
	ReservationReleased  ReservationStatus = "released"
	ReservationCommitted ReservationStatus = "committed"
)

type InventoryItem struct {
	ID                string
	ProductID         string
	VariantID         *string
	VendorID          string
	AvailableQuantity int64
	ReservedQuantity  int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ReservationLine is one line of a checkout's reservation request: how much
// of one product (or, if VariantID is set, one specific variant of it) an
// order needs held.
type ReservationLine struct {
	ProductID string
	VariantID *string
	Quantity  int64
}

type Reservation struct {
	ID              string
	InventoryItemID string
	ProductID       string
	VariantID       *string
	OrderID         string
	Quantity        int64
	Status          ReservationStatus
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

// ReservationTTL bounds how long a reservation holds stock before it should
// be considered abandoned. Releasing expired reservations back to available
// stock is a background sweep added when Asynq jobs land (phase 5); for now
// Order explicitly releases on cancel/payment-failure.
const ReservationTTL = 30 * time.Minute

func ValidateQuantity(quantity int64) error {
	if quantity <= 0 {
		return apperror.Validation("Quantity must be a positive number")
	}
	return nil
}
