package domain_test

import (
	"testing"

	"shopee/backend/services/payment/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
		want bool
	}{
		{domain.StatusPending, domain.StatusCaptured, true},   // auto-capturing provider
		{domain.StatusPending, domain.StatusAuthorized, true}, // authorize-then-capture provider
		{domain.StatusPending, domain.StatusFailed, true},
		{domain.StatusAuthorized, domain.StatusCaptured, true},
		{domain.StatusAuthorized, domain.StatusFailed, true},
		{domain.StatusCaptured, domain.StatusRefunded, true},
		{domain.StatusFailed, domain.StatusCaptured, false},   // failed is terminal
		{domain.StatusRefunded, domain.StatusCaptured, false}, // refunded is terminal
		{domain.StatusCaptured, domain.StatusPending, false},  // no going backwards
		{domain.StatusCaptured, domain.StatusFailed, false},   // a captured payment can only be refunded, not failed
	}

	for _, tt := range tests {
		if got := domain.CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestValidateAmount(t *testing.T) {
	if err := domain.ValidateAmount(0); err == nil {
		t.Error("expected an error for a zero amount")
	}
	if err := domain.ValidateAmount(-1); err == nil {
		t.Error("expected an error for a negative amount")
	}
	if err := domain.ValidateAmount(1); err != nil {
		t.Errorf("unexpected error for a positive amount: %v", err)
	}
}
