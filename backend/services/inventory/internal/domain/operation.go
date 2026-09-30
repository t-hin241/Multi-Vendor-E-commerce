package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"shopee/backend/pkg/apperror"
	"sort"
	"time"
)

type Operation struct {
	OrderID     string         `json:"order_id"`
	OperationID string         `json:"operation_id"`
	Status      string         `json:"status"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Legacy      bool           `json:"legacy"`
	Items       []*Reservation `json:"items"`
}

// NormalizeLines produces a stable payload identity and checks arithmetic before any stock write.
func NormalizeLines(lines []ReservationLine) ([]ReservationLine, string, error) {
	if len(lines) == 0 || len(lines) > 100 {
		return nil, "", apperror.Validation("Between 1 and 100 reservation lines are required")
	}
	grouped := map[string]ReservationLine{}
	for _, line := range lines {
		if line.ProductID == "" || line.VariantID != nil && *line.VariantID == "" {
			return nil, "", apperror.Validation("Product and variant identifiers must not be empty")
		}
		if err := ValidateQuantity(line.Quantity); err != nil {
			return nil, "", err
		}
		key := "p:" + line.ProductID
		if line.VariantID != nil {
			key = "v:" + *line.VariantID
		}
		old := grouped[key]
		if old.ProductID != "" && old.ProductID != line.ProductID {
			return nil, "", apperror.Validation("Variant cannot belong to multiple products")
		}
		if old.Quantity > math.MaxInt64-line.Quantity {
			return nil, "", apperror.Validation("Reservation quantity overflow")
		}
		line.Quantity += old.Quantity
		grouped[key] = line
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ReservationLine, 0, len(keys))
	for _, key := range keys {
		out = append(out, grouped[key])
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return out, hex.EncodeToString(sum[:]), nil
}

type OutboxEvent struct {
	ID      string `json:"id"`
	OrderID string `json:"order_id"`
	Type    string `json:"type"`
}
type OperationIssue struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Issue   string `json:"issue"`
	Legacy  bool   `json:"legacy"`
}
