package domain

// LineState explains whether a cart line can be checked out right now and,
// if not, why — so the buyer is told instead of the line silently vanishing
// or silently changing price.
type LineState string

const (
	LineAvailable LineState = "available"
	// LineRemoved: Catalog no longer has the product at all.
	LineRemoved LineState = "removed"
	// LineUnderReview: the product exists but is not approved (draft,
	// pending review, rejected after an edit).
	LineUnderReview LineState = "under_review"
	// LineNotForSale: approved, but not currently sellable — paused by the
	// vendor, or the shop itself is not allowed to sell (locked/suspended).
	// Catalog deliberately reports both the same way.
	LineNotForSale LineState = "not_for_sale"
	// LineOptionRequired: the product now has variants but the line was
	// added without one.
	LineOptionRequired LineState = "option_required"
	// LineOptionUnavailable: the selected variant no longer exists or no
	// longer belongs to this product.
	LineOptionUnavailable LineState = "option_unavailable"
	LineOutOfStock        LineState = "out_of_stock"
	// LineInsufficientStock: some stock exists, but less than the quantity
	// in the cart.
	LineInsufficientStock LineState = "insufficient_stock"
	// LineUnverified: Catalog could not be reached, so neither sellability
	// nor price is known. Never checked out on a stale guess.
	LineUnverified LineState = "unverified"
)

// Purchasable reports whether checkout may include a line in this state.
func (s LineState) Purchasable() bool { return s == LineAvailable }

// StockStatus is display-only: Inventory's reservation at checkout is what
// actually guarantees stock.
type StockStatus string

const (
	StockUnknown      StockStatus = "unknown"
	StockInStock      StockStatus = "in_stock"
	StockInsufficient StockStatus = "insufficient"
	StockOut          StockStatus = "out_of_stock"
)

// ProductFacts is what Cart learned from Catalog about a line's product.
type ProductFacts struct {
	Found       bool
	Status      string
	IsVisible   bool
	HasVariants bool
	PriceAmount int64
	Currency    string
}

// VariantFacts is what Cart learned from Catalog about a line's variant.
type VariantFacts struct {
	Found     bool
	ProductID string
}

// StockFacts is what Cart learned from Inventory. Known is false when
// Inventory could not be reached.
type StockFacts struct {
	Known     bool
	Available int64
}

// LineEvaluation is the outcome of EvaluateLine.
type LineEvaluation struct {
	State        LineState
	Stock        StockStatus
	PriceChanged bool
}

// EvaluateLine decides a line's state from what Catalog/Inventory reported.
// product is nil when Catalog could not be reached; variant is nil for a
// product-level line or when the variant lookup failed transiently (then
// variantLookupFailed is true).
func EvaluateLine(item *CartItem, product *ProductFacts, variant *VariantFacts, variantLookupFailed bool, stock StockFacts) LineEvaluation {
	eval := LineEvaluation{Stock: StockUnknown}
	if stock.Known {
		switch {
		case stock.Available <= 0:
			eval.Stock = StockOut
		case stock.Available < item.Quantity:
			eval.Stock = StockInsufficient
		default:
			eval.Stock = StockInStock
		}
	}

	switch {
	case product == nil:
		eval.State = LineUnverified
		return eval
	case !product.Found:
		eval.State = LineRemoved
		return eval
	}

	if item.SeenPriceAmount != nil && item.SeenCurrency != nil {
		eval.PriceChanged = *item.SeenPriceAmount != product.PriceAmount || *item.SeenCurrency != product.Currency
	}

	switch {
	case product.Status != "" && product.Status != "approved":
		eval.State = LineUnderReview
	case !product.IsVisible:
		eval.State = LineNotForSale
	case item.VariantID == nil && product.HasVariants:
		eval.State = LineOptionRequired
	case item.VariantID != nil && variantLookupFailed:
		eval.State = LineUnverified
	case item.VariantID != nil && (variant == nil || !variant.Found || variant.ProductID != item.ProductID):
		eval.State = LineOptionUnavailable
	case eval.Stock == StockOut:
		eval.State = LineOutOfStock
	case eval.Stock == StockInsufficient:
		eval.State = LineInsufficientStock
	default:
		eval.State = LineAvailable
	}
	return eval
}
