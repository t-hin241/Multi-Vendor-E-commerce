package domain_test

import (
	"testing"

	"shopee/backend/services/shipment/internal/domain"
)

func TestComputeShippingFee(t *testing.T) {
	rule := domain.FeeRule{BaseFeeAmount: 15000, FreeWeightGrams: 500, ExtraFeePerKg: 5000}

	tests := []struct {
		name        string
		weightGrams int64
		want        int64
	}{
		{"under the free allowance", 300, 15000},
		{"exactly at the free allowance", 500, 15000},
		{"1 gram over rounds up to a full extra kg", 501, 20000},
		{"exactly 1kg over the allowance", 1500, 20000},
		{"1 gram into the second extra kg rounds up again", 1501, 25000},
		{"2kg over the allowance", 2500, 25000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.ComputeShippingFee(rule, tt.weightGrams); got != tt.want {
				t.Errorf("ComputeShippingFee(%dg) = %d, want %d", tt.weightGrams, got, tt.want)
			}
		})
	}
}
