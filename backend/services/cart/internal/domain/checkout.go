package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// SnapshotLine is one cart line exactly as it was when Order started a
// checkout operation. LineID + LineVersion identify the line instance: a
// line removed and re-added later is a different line.
type SnapshotLine struct {
	LineID          string  `json:"line_id"`
	ProductID       string  `json:"product_id"`
	VariantID       *string `json:"variant_id,omitempty"`
	Quantity        int64   `json:"quantity"`
	LineVersion     int64   `json:"line_version"`
	SeenPriceAmount *int64  `json:"seen_price_amount,omitempty"`
	SeenCurrency    *string `json:"seen_currency,omitempty"`
}

// CheckoutOperation is the durable record of one checkout operation against
// a cart: the snapshot Order priced, and — once Order confirms the order
// exists — the receipt of consuming exactly those lines.
type CheckoutOperation struct {
	OperationID string
	BuyerID     string
	CartID      string
	CartVersion int64
	Lines       []SnapshotLine
	CreatedAt   time.Time
	ConsumeHash *string
	Receipt     *ConsumeReceipt
}

// Consumed reports whether the operation already has a consume receipt.
func (o *CheckoutOperation) Consumed() bool { return o.Receipt != nil }

// ConsumeLine is a purchased quantity of one snapshotted line.
type ConsumeLine struct {
	LineID   string `json:"line_id"`
	Quantity int64  `json:"quantity"`
}

type ConsumeOutcome string

const (
	// ConsumeRemoved: the whole line was bought and removed.
	ConsumeRemoved ConsumeOutcome = "removed"
	// ConsumeReduced: the buyer raised the quantity after the snapshot, so
	// only the purchased units were taken off and the rest stays.
	ConsumeReduced ConsumeOutcome = "reduced"
	// ConsumeAlreadyGone: the buyer removed the line (or the cart expired)
	// before consume ran; nothing else is touched.
	ConsumeAlreadyGone ConsumeOutcome = "already_gone"
)

type ConsumeLineResult struct {
	LineID            string         `json:"line_id"`
	Outcome           ConsumeOutcome `json:"outcome"`
	RemainingQuantity int64          `json:"remaining_quantity"`
}

// ConsumeReceipt is stored with the operation so a repeated consume returns
// the same answer instead of applying again.
type ConsumeReceipt struct {
	CartVersion int64               `json:"cart_version"`
	Lines       []ConsumeLineResult `json:"lines"`
	ConsumedAt  time.Time           `json:"consumed_at"`
}

// ValidateConsume checks a consume request only names lines from the
// snapshot, each at most once and never for more than was snapshotted.
func ValidateConsume(snapshot []SnapshotLine, lines []ConsumeLine) error {
	if len(lines) == 0 {
		return apperror.Validation("At least one purchased line is required")
	}
	if len(lines) > MaxLinesPerCart {
		return apperror.Validation("Too many purchased lines")
	}
	snapshotted := make(map[string]int64, len(snapshot))
	for _, s := range snapshot {
		snapshotted[s.LineID] = s.Quantity
	}
	seen := make(map[string]bool, len(lines))
	for _, l := range lines {
		max, ok := snapshotted[l.LineID]
		if !ok {
			return apperror.Validation("Line " + l.LineID + " is not part of this checkout snapshot")
		}
		if seen[l.LineID] {
			return apperror.Validation("Line " + l.LineID + " is listed more than once")
		}
		seen[l.LineID] = true
		if l.Quantity <= 0 || l.Quantity > max {
			return apperror.Validation("Purchased quantity for line " + l.LineID + " must be between 1 and the snapshotted quantity")
		}
	}
	return nil
}

// ConsumeHash fingerprints a consume request independent of line order, so
// a retry with the same lines is recognized as the same request.
func ConsumeHash(lines []ConsumeLine) string {
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, l.LineID+":"+strconv.FormatInt(l.Quantity, 10))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

// PlanConsume decides, per purchased line, what happens to the live cart:
// only the purchased units are taken off. current maps line id to the
// line's quantity now; a missing id means the line is gone. Units the buyer
// added after the snapshot survive, and lines not in the request are never
// touched.
func PlanConsume(current map[string]int64, lines []ConsumeLine) []ConsumeLineResult {
	results := make([]ConsumeLineResult, 0, len(lines))
	for _, l := range lines {
		qty, ok := current[l.LineID]
		switch {
		case !ok:
			results = append(results, ConsumeLineResult{LineID: l.LineID, Outcome: ConsumeAlreadyGone})
		case qty > l.Quantity:
			results = append(results, ConsumeLineResult{LineID: l.LineID, Outcome: ConsumeReduced, RemainingQuantity: qty - l.Quantity})
		default:
			results = append(results, ConsumeLineResult{LineID: l.LineID, Outcome: ConsumeRemoved})
		}
	}
	return results
}

// SnapshotFromItems freezes the cart's current lines for a checkout.
func SnapshotFromItems(items []*CartItem) ([]SnapshotLine, error) {
	if len(items) == 0 {
		return nil, apperror.Validation("Your cart is empty")
	}
	if len(items) > MaxLinesPerCart {
		return nil, apperror.Validation("Your cart has more than " + strconv.Itoa(MaxLinesPerCart) + " different items; please remove some before checking out")
	}
	lines := make([]SnapshotLine, 0, len(items))
	for _, item := range items {
		if item.Quantity > MaxQuantityPerLine {
			return nil, apperror.Validation("An item in your cart exceeds the per-item quantity limit; please update your cart")
		}
		lines = append(lines, SnapshotLine{
			LineID: item.ID, ProductID: item.ProductID, VariantID: item.VariantID,
			Quantity: item.Quantity, LineVersion: item.Version,
			SeenPriceAmount: item.SeenPriceAmount, SeenCurrency: item.SeenCurrency,
		})
	}
	return lines, nil
}
