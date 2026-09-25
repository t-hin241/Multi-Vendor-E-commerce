package domain

import "time"

// RestockStatus mirrors Vendor's own application-review shape (status +
// actor + timestamp + reason): a vendor's request to add stock to an
// already-approved product sits pending until an admin decides it, and that
// decision is terminal — no re-review path, same as every other moderation
// decision in this codebase.
type RestockStatus string

const (
	RestockPending  RestockStatus = "pending"
	RestockApproved RestockStatus = "approved"
	RestockRejected RestockStatus = "rejected"
)

// RestockRequest is a vendor's ask to add quantity units of stock to an
// existing inventory item. Unlike CreateItem (a product/variant's first-ever
// stock, set directly by the vendor with no review), this only applies to
// stock already set up for an approved product — see
// InventoryUseCase.RequestRestock.
type RestockRequest struct {
	ID                string
	InventoryItemID   string
	ProductID         string
	VariantID         *string
	VendorID          string
	RequestedQuantity int64
	Status            RestockStatus
	RequestedBy       string
	RejectionReason   *string
	DecidedBy         *string
	DecidedAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CanTransitionRestock enforces that only a pending request can be decided.
func CanTransitionRestock(from, to RestockStatus) bool {
	if from != RestockPending {
		return false
	}
	return to == RestockApproved || to == RestockRejected
}
