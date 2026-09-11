package domain_test

import (
	"testing"

	"shopee/backend/services/order/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
		want bool
	}{
		{domain.StatusPendingPayment, domain.StatusPaid, true},
		{domain.StatusPendingPayment, domain.StatusCancelled, true},
		{domain.StatusPendingPayment, domain.StatusShipped, false}, // can't ship before paid
		{domain.StatusPaid, domain.StatusProcessing, true},
		{domain.StatusProcessing, domain.StatusShipped, true},
		{domain.StatusShipped, domain.StatusCompleted, true},
		{domain.StatusCancelled, domain.StatusPaid, false}, // cancelled is terminal
		{domain.StatusRefunded, domain.StatusPaid, false},  // refunded is terminal
		{domain.StatusCompleted, domain.StatusRefunded, true},
		{domain.StatusPaid, domain.StatusPendingPayment, false}, // no going backwards
	}

	for _, tt := range tests {
		if got := domain.CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestAggregateStatus(t *testing.T) {
	tests := []struct {
		name     string
		statuses []domain.Status
		want     domain.Status
	}{
		{"no vendor orders yet", nil, domain.StatusPendingPayment},
		{"single vendor, just paid", []domain.Status{domain.StatusPaid}, domain.StatusPaid},
		{
			"weakest link: one vendor still paid, another already shipped",
			[]domain.Status{domain.StatusPaid, domain.StatusShipped},
			domain.StatusPaid,
		},
		{
			"all vendors shipped",
			[]domain.Status{domain.StatusShipped, domain.StatusShipped},
			domain.StatusShipped,
		},
		{
			"all vendors completed",
			[]domain.Status{domain.StatusCompleted, domain.StatusCompleted},
			domain.StatusCompleted,
		},
		{
			"a refunded vendor order doesn't hold back the rest",
			[]domain.Status{domain.StatusRefunded, domain.StatusShipped},
			domain.StatusShipped,
		},
		{
			"every vendor order refunded refunds the whole order",
			[]domain.Status{domain.StatusRefunded, domain.StatusRefunded},
			domain.StatusRefunded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.AggregateStatus(tt.statuses); got != tt.want {
				t.Errorf("AggregateStatus(%v) = %v, want %v", tt.statuses, got, tt.want)
			}
		})
	}
}

func TestComputeCommission(t *testing.T) {
	tests := []struct {
		subtotal       int64
		rateBps        int
		wantCommission int64
		wantNet        int64
	}{
		{subtotal: 100000, rateBps: 1000, wantCommission: 10000, wantNet: 90000}, // 10%
		{subtotal: 100000, rateBps: 0, wantCommission: 0, wantNet: 100000},       // no commission
		{subtotal: 100000, rateBps: 10000, wantCommission: 100000, wantNet: 0},   // 100%
		{subtotal: 999, rateBps: 1000, wantCommission: 99, wantNet: 900},         // rounds down, never over-charges the vendor
	}

	for _, tt := range tests {
		gotCommission, gotNet := domain.ComputeCommission(tt.subtotal, tt.rateBps)
		if gotCommission != tt.wantCommission || gotNet != tt.wantNet {
			t.Errorf("ComputeCommission(%d, %d) = (%d, %d), want (%d, %d)", tt.subtotal, tt.rateBps, gotCommission, gotNet, tt.wantCommission, tt.wantNet)
		}
		if gotCommission+gotNet != tt.subtotal {
			t.Errorf("commission + net must equal the subtotal exactly: got %d + %d != %d", gotCommission, gotNet, tt.subtotal)
		}
	}
}

func TestValidateCommissionRateBps(t *testing.T) {
	if err := domain.ValidateCommissionRateBps(-1); err == nil {
		t.Error("expected an error for a negative rate")
	}
	if err := domain.ValidateCommissionRateBps(10001); err == nil {
		t.Error("expected an error for a rate above 100%")
	}
	if err := domain.ValidateCommissionRateBps(1000); err != nil {
		t.Errorf("unexpected error for a valid rate: %v", err)
	}
}
