package repository

import (
	"errors"
	"fmt"
)

var ErrItemNotFound = errors.New("repository: inventory item not found")
var ErrItemAlreadyExists = errors.New("repository: inventory item already exists for this product")

// ErrProductNotStocked means a checkout tried to reserve a product that has
// no inventory item at all yet (the vendor never set an initial stock).
type ErrProductNotStocked struct {
	ProductID string
}

func (e *ErrProductNotStocked) Error() string {
	return fmt.Sprintf("repository: no inventory item for product %s", e.ProductID)
}

// ErrInsufficientStock means the requested quantity exceeds what's
// currently available (excluding what other reservations already hold) —
// exactly the "don't oversell" invariant.
type ErrInsufficientStock struct {
	ProductID string
	Available int64
	Requested int64
}

func (e *ErrInsufficientStock) Error() string {
	return fmt.Sprintf("repository: insufficient stock for product %s (available %d, requested %d)", e.ProductID, e.Available, e.Requested)
}
