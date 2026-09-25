package domain_test

import (
	"testing"

	"shopee/backend/services/vendorsvc/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
		want bool
	}{
		{domain.StatusPending, domain.StatusApproved, true},
		{domain.StatusPending, domain.StatusRejected, true},
		{domain.StatusApproved, domain.StatusRejected, false},
		{domain.StatusApproved, domain.StatusApproved, false},
		{domain.StatusRejected, domain.StatusApproved, false},
		{domain.StatusPending, domain.StatusPending, false},
	}

	for _, tt := range tests {
		got := domain.CanTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestValidateApplication(t *testing.T) {
	if err := domain.ValidateApplication("  "); err == nil {
		t.Error("expected an error for a blank shop name")
	}
	if err := domain.ValidateApplication("Alice's Shop"); err != nil {
		t.Errorf("unexpected error for a valid shop name: %v", err)
	}
}
