package domain_test

import (
	"errors"
	"math"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func TestQuantityRules(t *testing.T) {
	for _, q := range []int64{0, -5, domain.MaxQuantityPerLine + 1} {
		if domain.ValidateQuantity(q) == nil {
			t.Errorf("quantity %d must be rejected", q)
		}
	}
	if domain.ValidateSetQuantity(0) != nil {
		t.Error("setting 0 means remove and must be allowed")
	}
	if domain.ValidateSetQuantity(-1) == nil {
		t.Error("negative quantity must be rejected")
	}
	if _, err := domain.AccumulatedQuantity(domain.MaxQuantityPerLine-1, 2); err == nil {
		t.Error("accumulating past the limit must be rejected")
	}
	if got, err := domain.AccumulatedQuantity(3, 4); err != nil || got != 7 {
		t.Errorf("expected 7, got %d %v", got, err)
	}
	if domain.EnsureLineCapacity(domain.MaxLinesPerCart) == nil || domain.EnsureLineCapacity(domain.MaxLinesPerCart-1) != nil {
		t.Error("line capacity boundary is wrong")
	}
}

func TestMoneyArithmeticReportsOverflow(t *testing.T) {
	if _, ok := domain.LineSubtotal(math.MaxInt64/2+1, 2); ok {
		t.Error("expected overflow")
	}
	if v, ok := domain.LineSubtotal(150000, 3); !ok || v != 450000 {
		t.Errorf("expected 450000, got %d", v)
	}
	if _, ok := domain.AddAmounts(math.MaxInt64, 1); ok {
		t.Error("expected overflow")
	}
}

func TestCheckExpectedVersion(t *testing.T) {
	if domain.CheckExpectedVersion(nil, 7) != nil || domain.CheckExpectedVersion(ptr(int64(7)), 7) != nil {
		t.Error("missing or matching expectation must pass")
	}
	err := domain.CheckExpectedVersion(ptr(int64(6)), 7)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != domain.CodeCartChanged || appErr.Status != 409 {
		t.Fatalf("stale expectation must be a cart_changed conflict, got %v", err)
	}
}

func TestEvaluateLine(t *testing.T) {
	item := &domain.CartItem{ProductID: "p", Quantity: 2, SeenPriceAmount: ptr(int64(100)), SeenCurrency: ptr("VND")}
	variantItem := &domain.CartItem{ProductID: "p", VariantID: ptr("v"), Quantity: 2}
	ok := &domain.ProductFacts{Found: true, Status: "approved", IsVisible: true, PriceAmount: 100, Currency: "VND"}
	stock := domain.StockFacts{Known: true, Available: 10}

	cases := []struct {
		name    string
		item    *domain.CartItem
		product *domain.ProductFacts
		variant *domain.VariantFacts
		vFailed bool
		stock   domain.StockFacts
		want    domain.LineState
	}{
		{"available", item, ok, nil, false, stock, domain.LineAvailable},
		{"catalog down", item, nil, nil, false, stock, domain.LineUnverified},
		{"deleted", item, &domain.ProductFacts{}, nil, false, stock, domain.LineRemoved},
		{"under review", item, &domain.ProductFacts{Found: true, Status: "draft"}, nil, false, stock, domain.LineUnderReview},
		{"paused or shop locked", item, &domain.ProductFacts{Found: true, Status: "approved"}, nil, false, stock, domain.LineNotForSale},
		{"needs option", item, &domain.ProductFacts{Found: true, Status: "approved", IsVisible: true, HasVariants: true}, nil, false, stock, domain.LineOptionRequired},
		{"variant lookup failed", variantItem, ok, nil, true, stock, domain.LineUnverified},
		{"variant gone", variantItem, ok, &domain.VariantFacts{}, false, stock, domain.LineOptionUnavailable},
		{"variant moved", variantItem, ok, &domain.VariantFacts{Found: true, ProductID: "other"}, false, stock, domain.LineOptionUnavailable},
		{"out of stock", item, ok, nil, false, domain.StockFacts{Known: true}, domain.LineOutOfStock},
		{"short stock", item, ok, nil, false, domain.StockFacts{Known: true, Available: 1}, domain.LineInsufficientStock},
		{"stock unknown", item, ok, nil, false, domain.StockFacts{}, domain.LineAvailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.EvaluateLine(tc.item, tc.product, tc.variant, tc.vFailed, tc.stock).State; got != tc.want {
				t.Errorf("expected %s, got %s", tc.want, got)
			}
		})
	}

	moved := *ok
	moved.PriceAmount = 120
	if !domain.EvaluateLine(item, &moved, nil, false, stock).PriceChanged {
		t.Error("a new live price must be flagged")
	}
	if domain.EvaluateLine(&domain.CartItem{ProductID: "p", Quantity: 1}, &moved, nil, false, stock).PriceChanged {
		t.Error("a line without a price reference cannot be flagged")
	}
}

func TestConsumeRules(t *testing.T) {
	snapshot := []domain.SnapshotLine{{LineID: "a", Quantity: 2}, {LineID: "b", Quantity: 1}}
	bad := [][]domain.ConsumeLine{
		nil,
		{{LineID: "c", Quantity: 1}},
		{{LineID: "a", Quantity: 3}},
		{{LineID: "a", Quantity: 0}},
		{{LineID: "a", Quantity: 1}, {LineID: "a", Quantity: 1}},
	}
	for _, lines := range bad {
		if domain.ValidateConsume(snapshot, lines) == nil {
			t.Errorf("expected %v to be rejected", lines)
		}
	}
	if err := domain.ValidateConsume(snapshot, []domain.ConsumeLine{{LineID: "a", Quantity: 2}}); err != nil {
		t.Errorf("a subset of the snapshot is valid: %v", err)
	}

	h1 := domain.ConsumeHash([]domain.ConsumeLine{{LineID: "a", Quantity: 2}, {LineID: "b", Quantity: 1}})
	h2 := domain.ConsumeHash([]domain.ConsumeLine{{LineID: "b", Quantity: 1}, {LineID: "a", Quantity: 2}})
	h3 := domain.ConsumeHash([]domain.ConsumeLine{{LineID: "a", Quantity: 1}, {LineID: "b", Quantity: 1}})
	if h1 != h2 || h1 == h3 {
		t.Error("hash must ignore order and reflect quantities")
	}

	results := domain.PlanConsume(map[string]int64{"a": 5, "b": 1}, []domain.ConsumeLine{{LineID: "a", Quantity: 2}, {LineID: "b", Quantity: 1}, {LineID: "c", Quantity: 1}})
	want := []domain.ConsumeLineResult{
		{LineID: "a", Outcome: domain.ConsumeReduced, RemainingQuantity: 3},
		{LineID: "b", Outcome: domain.ConsumeRemoved},
		{LineID: "c", Outcome: domain.ConsumeAlreadyGone},
	}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("line %d: expected %+v, got %+v", i, want[i], results[i])
		}
	}
}

func TestSnapshotFromItems(t *testing.T) {
	if _, err := domain.SnapshotFromItems(nil); err == nil {
		t.Error("an empty cart cannot be snapshotted")
	}
	lines, err := domain.SnapshotFromItems([]*domain.CartItem{{ID: "l1", ProductID: "p", Quantity: 2, Version: 3}})
	if err != nil || len(lines) != 1 || lines[0].LineVersion != 3 {
		t.Fatalf("unexpected snapshot %+v %v", lines, err)
	}
}
