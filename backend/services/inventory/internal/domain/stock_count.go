package domain

import (
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// StockCount is a vendor's physical count (kiểm kê) of one stock item. It
// can only write stock down (lost, damaged or miscounted units); adding
// stock always goes through a restock request an admin approves.
//
// On-hand units are available + reserved: reserved units are held for
// pending orders and are never changed by a count. The count therefore
// sets available = counted_on_hand - reserved.
type StockCount struct {
	ID                string    `json:"id"`
	InventoryItemID   string    `json:"inventory_item_id"`
	CountedOnHand     int64     `json:"counted_on_hand"`
	PreviousAvailable int64     `json:"previous_available"`
	NewAvailable      int64     `json:"new_available"`
	ReservedAtCount   int64     `json:"reserved_at_count"`
	ActorUserID       string    `json:"actor_user_id"`
	Reason            string    `json:"reason"`
	CreatedAt         time.Time `json:"created_at"`
}

const maxStockCountReason = 500

// ValidateStockCountInput checks the parts of a count that do not depend
// on the current stock.
func ValidateStockCountInput(countedOnHand int64, reason string) (string, error) {
	if countedOnHand < 0 {
		return "", apperror.Validation("Counted quantity cannot be negative")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxStockCountReason {
		return "", apperror.Validation("A reason of at most 500 bytes is required")
	}
	return reason, nil
}

// PlanStockCount computes the new available quantity for a count against
// the item's current balance, refusing anything that would touch reserved
// units or add stock.
func PlanStockCount(available, reserved, countedOnHand int64) (int64, error) {
	if countedOnHand < reserved {
		return 0, apperror.Conflict("The count is below the " + strconv.FormatInt(reserved, 10) +
			" units held for pending orders; resolve those orders before recording this count")
	}
	next := countedOnHand - reserved
	if next > available {
		return 0, apperror.Conflict("The count is higher than recorded stock; request a restock to add units")
	}
	return next, nil
}

// SameStockCount reports whether a retried count carries the same payload
// as the stored one, so a lost response can be retried safely.
func SameStockCount(stored *StockCount, itemID string, countedOnHand int64, reason string) bool {
	return stored.InventoryItemID == itemID && stored.CountedOnHand == countedOnHand && stored.Reason == reason
}
