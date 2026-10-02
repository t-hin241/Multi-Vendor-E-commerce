package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shared with the Python seed tooling (data tooling plan 14, DEV-02): the
// scraper's ports of these rules must produce what the backend would.
type parityVectors struct {
	Slug []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"slug"`
	VariantKey []struct {
		In  [][2]string `json:"in"`
		Out string      `json:"out"`
	} `json:"variant_key"`
	SKU []struct {
		In     string `json:"in"`
		Repeat int    `json:"repeat"`
		Valid  bool   `json:"valid"`
	} `json:"sku"`
	ProductInput []struct {
		Name              string `json:"name"`
		NameRepeat        int    `json:"name_repeat"`
		Description       string `json:"description"`
		DescriptionRepeat int    `json:"description_repeat"`
		Price             int64  `json:"price"`
		Valid             bool   `json:"valid"`
	} `json:"product_input"`
	AttributeOptionValue []struct {
		In    string `json:"in"`
		Valid bool   `json:"valid"`
	} `json:"attribute_option_value"`
	Money []struct {
		Out *json.Number `json:"out"`
	} `json:"money"`
}

func loadParity(t *testing.T) parityVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "seed_parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v parityVectors
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func repeat(s string, n int) string {
	if n == 0 {
		return s
	}
	return strings.Repeat(s, n)
}

func TestSeedParityWithPythonPorts(t *testing.T) {
	v := loadParity(t)
	for _, c := range v.Slug {
		if got := Slugify(c.In); got != c.Out {
			t.Errorf("Slugify(%q) = %q, vectors say %q", c.In, got, c.Out)
		}
	}
	for _, c := range v.VariantKey {
		selections := make([]VariantOptionSelection, 0, len(c.In))
		for _, p := range c.In {
			selections = append(selections, VariantOptionSelection{AttributeID: p[0], OptionID: p[1]})
		}
		if got := BuildVariantKey(selections); got != c.Out {
			t.Errorf("BuildVariantKey(%v) = %q, vectors say %q", c.In, got, c.Out)
		}
	}
	for _, c := range v.SKU {
		sku := repeat(c.In, c.Repeat)
		if valid := ValidateSKU(sku) == nil; valid != c.Valid {
			t.Errorf("ValidateSKU(%d bytes) valid=%v, vectors say %v", len(sku), valid, c.Valid)
		}
	}
	for _, c := range v.ProductInput {
		name, description := repeat(c.Name, c.NameRepeat), repeat(c.Description, c.DescriptionRepeat)
		if valid := ValidateProductInput(name, description, c.Price) == nil; valid != c.Valid {
			t.Errorf("ValidateProductInput(%d-byte name, %d-byte description, %d) valid=%v, vectors say %v",
				len(name), len(description), c.Price, valid, c.Valid)
		}
	}
	for _, c := range v.AttributeOptionValue {
		if valid := ValidateAttributeOptionValue(c.In) == nil; valid != c.Valid {
			t.Errorf("ValidateAttributeOptionValue(%q) valid=%v, vectors say %v", c.In, valid, c.Valid)
		}
	}
	// A money value the seed produces is an integer amount of minor units
	// that fits the backend's BIGINT/int64.
	for _, c := range v.Money {
		if c.Out == nil {
			continue
		}
		if _, err := c.Out.Int64(); err != nil {
			t.Errorf("money %s does not fit int64: %v", c.Out, err)
		}
	}
}
