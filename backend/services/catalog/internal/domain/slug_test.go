package domain_test

import (
	"testing"

	"shopee/backend/services/catalog/internal/domain"
)

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"Alice's Handmade Shop": "alice-s-handmade-shop",
		"  Trim Me  ":           "trim-me",
		"Already-slug":          "already-slug",
		"Ao Thun 100% Cotton":   "ao-thun-100-cotton",
	}

	for input, want := range tests {
		if got := domain.Slugify(input); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
}
