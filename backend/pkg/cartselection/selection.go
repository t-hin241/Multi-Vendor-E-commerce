// Package cartselection defines the selection wire contract shared by Cart and Order.
package cartselection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
)

// IDs is nil only for the legacy all-cart request. Explicit null/empty is invalid.
type IDs []string

func (ids *IDs) UnmarshalJSON(data []byte) error {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return Invalid()
	}
	if values == nil {
		return Invalid()
	}
	if err := Validate(values); err != nil {
		return err
	}
	*ids = values
	return nil
}

func Validate(ids []string) error {
	if ids == nil {
		return nil
	}
	if len(ids) == 0 || len(ids) > 50 {
		return Invalid()
	}
	seen := map[string]bool{}
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || seen[id] {
			return Invalid()
		}
		seen[id] = true
	}
	return nil
}

func Hash(ids []string) string {
	if ids == nil {
		return ""
	} // preserve legacy request hashes
	ordered := append([]string{}, ids...)
	sort.Strings(ordered)
	sum := sha256.Sum256([]byte(strings.Join(ordered, ",")))
	return hex.EncodeToString(sum[:])
}

func Invalid() *apperror.Error {
	return &apperror.Error{Code: "invalid_selection", Status: 422, Message: "selected_line_ids must contain 1 to 50 unique cart line IDs owned by this buyer"}
}

func Stale() *apperror.Error {
	return &apperror.Error{Code: "selection_stale", Status: 409, Message: "The cart changed; reload it and review your selection"}
}

func Disabled() *apperror.Error {
	return &apperror.Error{Code: "selective_checkout_disabled", Status: 409, Message: "Selective checkout is unavailable; review your cart before checking out"}
}

func Claimed() *apperror.Error {
	return &apperror.Error{Code: "line_claimed", Status: 409, Message: "A checkout is already using this cart; check your orders before trying again"}
}
