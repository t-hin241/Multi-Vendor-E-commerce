package domain

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// BulkTarget is a product, or one SKU of it, of one shop as AF-16 imports
// and exports see it.
type BulkTarget struct {
	ProductID      string `json:"product_id"`
	ProductName    string `json:"product_name"`
	ProductVersion int64  `json:"product_version"`
	ProductPrice   int64  `json:"product_price"`
	ProductStatus  Status `json:"product_status"`
	HasVariants    bool   `json:"has_variants"`
	VariantID      string `json:"variant_id,omitempty"`
	SKU            string `json:"sku,omitempty"`
	VariantPrice   int64  `json:"variant_price,omitempty"`
	PriceVersion   int64  `json:"price_version,omitempty"`
}

var (
	clientRowID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	wholeNumber = regexp.MustCompile(`^[0-9]{1,18}$`)
)

// ValidClientRowID is the vendor's stable id of a product row in imports.
func ValidClientRowID(v string) bool { return clientRowID.MatchString(v) }

// ParseImportAmount accepts a positive whole number of VND (minor unit):
// no decimals, separators, signs or exponents.
func ParseImportAmount(v string) (int64, bool) {
	if !wholeNumber.MatchString(v) {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n > 0
}

// PlainTextDescription turns an imported plain-text description into the
// editor's HTML: escaped paragraphs, line breaks kept. Imports never carry
// markup or media.
func PlainTextDescription(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		lines := strings.Split(para, "\n")
		for i, l := range lines {
			lines[i] = html.EscapeString(strings.TrimSpace(l))
		}
		b.WriteString("<p>" + strings.Join(lines, "<br>") + "</p>")
	}
	return b.String()
}
