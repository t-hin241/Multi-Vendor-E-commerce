package transport

import (
	"time"

	"shopee/backend/services/inventory/internal/domain"
)

// createItemRequest requires exactly one of ProductID/VariantID (checked in
// the handler, not via binding, since Gin can't express an XOR of two
// optional fields declaratively).
type createItemRequest struct {
	ProductID       *string `json:"product_id" binding:"omitempty,uuid"`
	VariantID       *string `json:"variant_id" binding:"omitempty,uuid"`
	InitialQuantity int64   `json:"initial_quantity"`
}

type restockRequest struct {
	Quantity int64 `json:"quantity" binding:"required,gt=0"`
}

type itemResponse struct {
	ID                string    `json:"id"`
	ProductID         string    `json:"product_id" binding:"omitempty,uuid"`
	VariantID         *string   `json:"variant_id,omitempty"`
	VendorID          string    `json:"vendor_id"`
	AvailableQuantity int64     `json:"available_quantity"`
	ReservedQuantity  int64     `json:"reserved_quantity"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toItemResponse(i *domain.InventoryItem) itemResponse {
	return itemResponse{
		ID:                i.ID,
		ProductID:         i.ProductID,
		VariantID:         i.VariantID,
		VendorID:          i.VendorID,
		AvailableQuantity: i.AvailableQuantity,
		ReservedQuantity:  i.ReservedQuantity,
		CreatedAt:         i.CreatedAt,
		UpdatedAt:         i.UpdatedAt,
	}
}

func toItemResponseList(items []*domain.InventoryItem) []itemResponse {
	out := make([]itemResponse, 0, len(items))
	for _, i := range items {
		out = append(out, toItemResponse(i))
	}
	return out
}

type reserveLineRequest struct {
	ProductID string  `json:"product_id" binding:"required,uuid"`
	VariantID *string `json:"variant_id" binding:"omitempty,uuid"`
	Quantity  int64   `json:"quantity" binding:"required,gt=0"`
}

type reserveRequest struct {
	OrderID string               `json:"order_id" binding:"required,uuid"`
	Items   []reserveLineRequest `json:"items" binding:"required,min=1,max=100,dive"`
}

type returnRestockRequest struct {
	ReturnID  string  `json:"return_id" binding:"required,uuid"`
	ProductID string  `json:"product_id" binding:"required,uuid"`
	VariantID *string `json:"variant_id" binding:"omitempty,uuid"`
	Quantity  int64   `json:"quantity" binding:"required,min=1"`
}

// recoveryRestockRequest puts back units that never left the warehouse.
type recoveryRestockRequest struct {
	RecoveryID string  `json:"recovery_id" binding:"required,max=120"`
	ProductID  string  `json:"product_id" binding:"required,uuid"`
	VariantID  *string `json:"variant_id" binding:"omitempty,uuid"`
	Quantity   int64   `json:"quantity" binding:"required,min=1"`
}

type releaseRequest struct {
	OrderID string `json:"order_id" binding:"required,uuid"`
}

type variantStockResponse struct {
	VariantID         string `json:"variant_id" binding:"omitempty,uuid"`
	AvailableQuantity int64  `json:"available_quantity"`
}

func toVariantStockResponseList(stock map[string]int64) []variantStockResponse {
	out := make([]variantStockResponse, 0, len(stock))
	for variantID, qty := range stock {
		out = append(out, variantStockResponse{VariantID: variantID, AvailableQuantity: qty})
	}
	return out
}

type restockRequestResponse struct {
	ID                string     `json:"id"`
	ProductID         string     `json:"product_id" binding:"omitempty,uuid"`
	VariantID         *string    `json:"variant_id,omitempty"`
	VendorID          string     `json:"vendor_id"`
	RequestedQuantity int64      `json:"requested_quantity"`
	Status            string     `json:"status"`
	RejectionReason   *string    `json:"rejection_reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
}

func toRestockRequestResponse(r *domain.RestockRequest) restockRequestResponse {
	return restockRequestResponse{
		ID:                r.ID,
		ProductID:         r.ProductID,
		VariantID:         r.VariantID,
		VendorID:          r.VendorID,
		RequestedQuantity: r.RequestedQuantity,
		Status:            string(r.Status),
		RejectionReason:   r.RejectionReason,
		CreatedAt:         r.CreatedAt,
		DecidedAt:         r.DecidedAt,
	}
}

func toRestockRequestResponseList(requests []*domain.RestockRequest) []restockRequestResponse {
	out := make([]restockRequestResponse, 0, len(requests))
	for _, r := range requests {
		out = append(out, toRestockRequestResponse(r))
	}
	return out
}

type rejectRestockRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// stockReadinessResponse backs Catalog's SubmitForReview completeness
// check.
type stockReadinessResponse struct {
	Ready bool `json:"ready"`
}

// productStockResponse backs Catalog's admin moderation detail view for a
// non-variant product.
type productStockResponse struct {
	AvailableQuantity int64 `json:"available_quantity"`
	Exists            bool  `json:"exists"`
}
