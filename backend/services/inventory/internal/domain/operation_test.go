package domain_test

import (
	"math"
	"shopee/backend/services/inventory/internal/domain"
	"testing"
)

func TestNormalizeReservationPayload(t *testing.T) {
	a := domain.ReservationLine{ProductID: "test-product", Quantity: 2}
	b := a
	b.Quantity = 3
	merged, hash, err := domain.NormalizeLines([]domain.ReservationLine{a, b})
	if err != nil {
		t.Fatal(err)
	}
	_, same, err := domain.NormalizeLines([]domain.ReservationLine{{ProductID: a.ProductID, Quantity: 5}})
	if err != nil || same != hash || len(merged) != 1 {
		t.Fatal("payload identity depends on duplicate line layout")
	}
	_, different, _ := domain.NormalizeLines([]domain.ReservationLine{a})
	if different == hash {
		t.Fatal("different quantity has same identity")
	}
	a.Quantity = math.MaxInt64
	if _, _, err := domain.NormalizeLines([]domain.ReservationLine{a, b}); err == nil {
		t.Fatal("quantity overflow accepted")
	}
	variant := "test-variant"
	a.Quantity = 1
	a.VariantID = &variant
	b.VariantID = &variant
	b.ProductID = "other-product"
	if _, _, err := domain.NormalizeLines([]domain.ReservationLine{a, b}); err == nil {
		t.Fatal("variant mapped to conflicting products")
	}
	for _, qty := range []int64{0, -1} {
		if _, _, err := domain.NormalizeLines([]domain.ReservationLine{{ProductID: "test", Quantity: qty}}); err == nil {
			t.Fatal("invalid quantity accepted")
		}
	}
}
