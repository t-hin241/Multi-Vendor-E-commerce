package domain

import (
	"sort"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// ProductVariant is one specific combination of variant-defining attribute
// option values for a product (e.g. Color=Red, Size=L), with its own SKU
// and its own stock (tracked by Inventory, keyed off this id).
type ProductVariant struct {
	ID         string
	ProductID  string
	SKU        string
	VariantKey string
	CreatedAt  time.Time
}

// VariantOptionSelection is one axis=value pair of a variant, e.g.
// {AttributeID: "color-attr-id", OptionID: "red-option-id"}.
type VariantOptionSelection struct {
	AttributeID string
	OptionID    string
}

// VariantOptionDetail is a VariantOptionSelection resolved to its display
// labels (e.g. "Color" / "Red"), for the vendor console's variant listing.
type VariantOptionDetail struct {
	AttributeID   string
	AttributeName string
	OptionID      string
	OptionValue   string
}

// VariantView is a variant resolved for display, including its current
// stock — the public product-detail page's per-variant read model. A
// separate, richer projection from the vendor console's plain
// ProductVariant + []VariantOptionDetail pair, since only buyers need
// AvailableQuantity.
type VariantView struct {
	Variant           ProductVariant
	Options           []VariantOptionDetail
	AvailableQuantity int64
}

func ValidateSKU(sku string) error {
	if strings.TrimSpace(sku) == "" {
		return apperror.Validation("SKU is required")
	}
	return nil
}

// BuildVariantKey computes a stable, order-independent key identifying a
// specific combination of option selections, so
// UNIQUE(product_id, variant_key) can catch a vendor accidentally creating
// the same combination (e.g. Red/L) twice for the same product — Postgres
// has no direct way to enforce uniqueness across the multiple rows a
// variant's options span, so the key is computed once here instead.
func BuildVariantKey(selections []VariantOptionSelection) string {
	pairs := make([]string, 0, len(selections))
	for _, s := range selections {
		pairs = append(pairs, s.AttributeID+":"+s.OptionID)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "|")
}
