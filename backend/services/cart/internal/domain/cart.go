// Package domain holds Cart's entities and business rules. A cart never
// snapshots price — it only ever points at a product id and a quantity;
// pricing is snapshotted once, at checkout, by Order.
package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

type Cart struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CartItem struct {
	ID        string
	CartID    string
	ProductID string
	VariantID *string
	Quantity  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func ValidateQuantity(quantity int64) error {
	if quantity <= 0 {
		return apperror.Validation("Quantity must be a positive number")
	}
	return nil
}
