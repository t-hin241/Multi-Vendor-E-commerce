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
	ReservationExpired   ReservationStatus = "expired"
)

type InventoryItem struct {
	ActorUserID       string
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
	ID              string            `json:"id"`
	InventoryItemID string            `json:"inventory_item_id"`
	ProductID       string            `json:"product_id"`
	VariantID       *string           `json:"variant_id"`
	OrderID         string            `json:"order_id"`
	Quantity        int64             `json:"quantity"`
	Status          ReservationStatus `json:"status"`
	ExpiresAt       time.Time         `json:"expires_at"`
	CreatedAt       time.Time         `json:"created_at"`
}

// ReservationTTL is the payment hold deadline shared through operation receipts.
const ReservationTTL = 30 * time.Minute

func ValidateQuantity(quantity int64) error {
	if quantity <= 0 {
		return apperror.Validation("Quantity must be a positive number")
	}
	return nil
}
