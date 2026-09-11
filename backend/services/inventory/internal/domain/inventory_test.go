package domain_test

import (
	"testing"

	"shopee/backend/services/inventory/internal/domain"
)

func TestValidateQuantity(t *testing.T) {
	if err := domain.ValidateQuantity(0); err == nil {
		t.Error("expected an error for a zero quantity")
	}
	if err := domain.ValidateQuantity(-5); err == nil {
		t.Error("expected an error for a negative quantity")
	}
	if err := domain.ValidateQuantity(1); err != nil {
		t.Errorf("unexpected error for a positive quantity: %v", err)
	}
}
