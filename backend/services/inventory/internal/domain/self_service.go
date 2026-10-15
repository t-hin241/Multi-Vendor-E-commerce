package domain

import (
	"math"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type StockAdjustment struct {
	ItemID          string `json:"item_id"`
	Kind            string `json:"kind"`
	QuantityDelta   int64  `json:"quantity_delta"`
	PhysicalCount   *int64 `json:"physical_count,omitempty"`
	Reason          string `json:"reason"`
	EvidenceRef     string `json:"evidence_ref"`
	ExpectedVersion int64  `json:"expected_version"`
}

func StockInputError(message string) error {
	return &apperror.Error{Code: "validation_error", Status: 422, Message: message}
}
func StockConflict(code, message string) error {
	return &apperror.Error{Code: apperror.Code(code), Status: 409, Message: message}
}
func (a *StockAdjustment) Normalize() error {
	a.Reason = strings.TrimSpace(a.Reason)
	a.EvidenceRef = strings.TrimSpace(a.EvidenceRef)
	if a.ExpectedVersion < 1 || len(a.Reason) < 3 || len(a.Reason) > 500 || len(a.EvidenceRef) < 1 || len(a.EvidenceRef) > 120 || strings.ContainsAny(a.EvidenceRef, "\r\n\t") {
		return StockInputError("expected_version, reason (3-500 bytes) and evidence_ref (1-120 bytes) are required")
	}
	switch a.Kind {
	case "receipt":
		if a.QuantityDelta <= 0 {
			return StockInputError("receipt quantity_delta must be positive")
		}
	case "damage":
		if a.QuantityDelta >= 0 {
			return StockInputError("damage quantity_delta must be negative")
		}
	case "correction":
		if a.QuantityDelta == 0 {
			return StockInputError("correction quantity_delta must be nonzero")
		}
	case "count":
		if a.PhysicalCount == nil || *a.PhysicalCount < 0 || a.QuantityDelta != 0 {
			return StockInputError("physical_count must be non-negative; quantity_delta must be zero")
		}
	default:
		return StockInputError("kind must be receipt, damage or correction; returned goods require inspected recovery")
	}
	if a.Kind != "count" && a.PhysicalCount != nil {
		return StockInputError("physical_count is only valid for counts")
	}
	return nil
}

// PlanAdjustment never changes holds and checks arithmetic before adding.
func PlanAdjustment(a StockAdjustment, available, reserved int64) (int64, error) {
	delta := a.QuantityDelta
	if a.Kind == "count" {
		delta = *a.PhysicalCount - (available + reserved)
	}
	if delta < -available {
		return 0, StockConflict("reserved_stock_conflict", "Physical stock cannot be reduced below reserved stock; investigate the shortage")
	}
	if delta > math.MaxInt64-available-reserved {
		return 0, StockInputError("Stock quantity exceeds the supported maximum")
	}
	return delta, nil
}

type StockPolicy struct {
	Enabled        bool        `json:"enabled"`
	Limits         StockLimits `json:"limits"`
	Version        int64       `json:"version"`
	FeatureEnabled bool        `json:"feature_enabled"`
}
type StockLimits struct {
	Receipt int64 `json:"receipt"`
}
type PolicyChange struct {
	Enabled         bool        `json:"enabled"`
	Limits          StockLimits `json:"limits"`
	Reason          string      `json:"reason"`
	ExpectedVersion int64       `json:"expected_version"`
}
type AlertSettings struct {
	LowStockThreshold int64 `json:"low_stock_threshold"`
	Enabled           bool  `json:"enabled"`
	ExpectedVersion   int64 `json:"expected_version"`
}
type AdjustmentReceipt struct {
	FirstApprovedBy *string `json:"first_approved_by,omitempty"`
	OperationID     string  `json:"operation_id"`
	Status          string  `json:"status"`
	StatusURL       string  `json:"status_url"`
	MovementID      string  `json:"movement_id,omitempty"`
	OnHand          int64   `json:"on_hand"`
	Reserved        int64   `json:"reserved"`
	Available       int64   `json:"available"`
	Version         int64   `json:"version"`
	PolicyVersion   int64   `json:"policy_version"`
}
type StockMovement struct {
	ID            string    `json:"id"`
	ItemID        string    `json:"item_id"`
	QuantityDelta int64     `json:"quantity_delta"`
	ReservedDelta int64     `json:"reserved_delta"`
	Kind          string    `json:"kind"`
	ActorID       *string   `json:"actor_user_id,omitempty"`
	Source        string    `json:"source"`
	Reason        string    `json:"reason"`
	EvidenceRef   string    `json:"evidence_ref"`
	CreatedAt     time.Time `json:"created_at"`
}
type AdjustmentWork struct {
	FirstApprovedBy *string `json:"first_approved_by,omitempty"`
	ID              string  `json:"id"`
	VendorID        string  `json:"vendor_id"`
	ActorID         string  `json:"actor_user_id"`
	StockAdjustment
	PolicyVersion int64     `json:"policy_version"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}
type StockPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type StockAlertWork struct {
	ID        string    `json:"id"`
	ItemID    string    `json:"item_id"`
	VendorID  string    `json:"vendor_id"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
}

// CatalogTarget is a product, or one SKU of it, of one shop as Catalog
// resolves it for AF-16 stock imports.
type CatalogTarget struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	HasVariants bool   `json:"has_variants"`
	VariantID   string `json:"variant_id,omitempty"`
	SKU         string `json:"sku,omitempty"`
}
