package domain

// CheckoutSnapshot is a bounded, fresh read of Catalog-owned facts. It
// contains no inventory or cached selling permissions. Missing IDs are
// omitted; the caller must reject a cart line whose facts are absent.
type CheckoutSnapshot struct {
	Products []CheckoutProduct
	Variants []CheckoutVariant
}

type CheckoutProduct struct {
	ID                 string `json:"id"`
	VendorID           string `json:"vendor_id"`
	Name               string `json:"name"`
	PriceAmount        int64  `json:"price_amount"`
	Currency           string `json:"currency"`
	Status             Status `json:"status"`
	IsActive           bool   `json:"is_active"`
	Version            int64  `json:"version"`
	EnforcedVersion    int64  `json:"enforced_version"`
	HasVariants        bool   `json:"has_variants"`
	PackageWeightGrams *int64 `json:"package_weight_grams"`
}

type CheckoutVariant struct {
	ID        string                  `json:"id"`
	ProductID string                  `json:"product_id"`
	VendorID  string                  `json:"vendor_id"`
	SKU       string                  `json:"sku"`
	Options   []CheckoutVariantOption `json:"options"`
}

type CheckoutVariantOption struct {
	AttributeID   string `json:"attribute_id"`
	AttributeName string `json:"attribute_name"`
	OptionID      string `json:"option_id"`
	OptionValue   string `json:"option_value"`
}
