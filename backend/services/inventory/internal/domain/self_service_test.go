package domain_test

import (
	"math"
	"shopee/backend/services/inventory/internal/domain"
	"testing"
)

func TestAdjustmentArithmeticPreservesHolds(t *testing.T) {
	count := int64(2)
	for _, tc := range []struct {
		name                      string
		a                         domain.StockAdjustment
		available, reserved, want int64
		bad                       bool
	}{
		{"receipt", domain.StockAdjustment{Kind: "receipt", QuantityDelta: 10}, 7, 3, 10, false},
		{"damage", domain.StockAdjustment{Kind: "damage", QuantityDelta: -7}, 7, 3, -7, false},
		{"holds", domain.StockAdjustment{Kind: "damage", QuantityDelta: -8}, 7, 3, 0, true},
		{"underflow", domain.StockAdjustment{Kind: "damage", QuantityDelta: math.MinInt64}, 7, 3, 0, true},
		{"overflow", domain.StockAdjustment{Kind: "receipt", QuantityDelta: math.MaxInt64}, 7, 3, 0, true},
		{"count below holds", domain.StockAdjustment{Kind: "count", PhysicalCount: &count}, 7, 3, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.PlanAdjustment(tc.a, tc.available, tc.reserved)
			if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
				t.Fatalf("delta=%d err=%v", got, err)
			}
		})
	}
}
func TestAdjustmentRejectsReturnAndInvalidInput(t *testing.T) {
	for _, kind := range []string{"return", "recovery", "receipt", "damage", "correction", "count"} {
		a := domain.StockAdjustment{Kind: kind, Reason: "counted", EvidenceRef: "fake-document", ExpectedVersion: 1}
		if err := a.Normalize(); err == nil {
			t.Fatalf("accepted invalid %s", kind)
		}
	}
	a := domain.StockAdjustment{Kind: "receipt", QuantityDelta: 1, Reason: "  received  ", EvidenceRef: " fake-document ", ExpectedVersion: 1}
	if err := a.Normalize(); err != nil || a.Reason != "received" || a.EvidenceRef != "fake-document" {
		t.Fatalf("normalize: %+v %v", a, err)
	}
}
