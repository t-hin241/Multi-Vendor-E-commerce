package domain

// Packaging is a product's shipping dimensions, captured once at the
// product level (not per-variant in this pass) — weight in grams,
// dimensions in millimeters, matching the codebase's fixed-point-integer
// convention for anything that matters (money, quantities). A field stays
// nil when its category's attribute template didn't ask for it.
type Packaging struct {
	WeightGrams *int64
	LengthMM    *int64
	WidthMM     *int64
	HeightMM    *int64
}

// IsEmpty reports whether none of the four fields were ever supplied —
// used to skip writing an all-nil row for a category that requires no
// packaging fields at all.
func (p Packaging) IsEmpty() bool {
	return p.WeightGrams == nil && p.LengthMM == nil && p.WidthMM == nil && p.HeightMM == nil
}

// PackagingAttributeCode identifies which of the four reserved attributes
// (seeded by migration 000009) a resolved template entry is — the catalog
// usecase uses this to route a submitted value into Packaging instead of
// the generic ProductAttributeValue table.
type PackagingAttributeCode string

const (
	PackagingCodeWeight PackagingAttributeCode = "pkg_weight"
	PackagingCodeLength PackagingAttributeCode = "pkg_length"
	PackagingCodeWidth  PackagingAttributeCode = "pkg_width"
	PackagingCodeHeight PackagingAttributeCode = "pkg_height"
)

// IsPackagingCode reports whether an attribute code is one of the four
// reserved packaging fields.
func IsPackagingCode(code string) bool {
	switch PackagingAttributeCode(code) {
	case PackagingCodeWeight, PackagingCodeLength, PackagingCodeWidth, PackagingCodeHeight:
		return true
	default:
		return false
	}
}
