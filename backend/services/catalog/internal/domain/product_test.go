package domain_test

import (
	"testing"

	"shopee/backend/services/catalog/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
		want bool
	}{
		{domain.StatusPendingReview, domain.StatusApproved, true},
		{domain.StatusPendingReview, domain.StatusRejected, true},
		{domain.StatusApproved, domain.StatusRejected, false},
		{domain.StatusRejected, domain.StatusApproved, false},
	}

	for _, tt := range tests {
		if got := domain.CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestIsPubliclyVisible(t *testing.T) {
	tests := []struct {
		name   string
		status domain.Status
		active bool
		want   bool
	}{
		{"approved and active", domain.StatusApproved, true, true},
		{"approved but inactive", domain.StatusApproved, false, false},
		{"pending review", domain.StatusPendingReview, true, false},
		{"rejected", domain.StatusRejected, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &domain.Product{Status: tt.status, IsActive: tt.active}
			if got := p.IsPubliclyVisible(); got != tt.want {
				t.Errorf("IsPubliclyVisible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateProductInput(t *testing.T) {
	if err := domain.ValidateProductInput("", "desc", 1000); err == nil {
		t.Error("expected an error for an empty name")
	}
	if err := domain.ValidateProductInput("Shoe", "desc", 0); err == nil {
		t.Error("expected an error for a zero price")
	}
	if err := domain.ValidateProductInput("Shoe", "desc", -100); err == nil {
		t.Error("expected an error for a negative price")
	}
	if err := domain.ValidateProductInput("Shoe", "desc", 100000); err != nil {
		t.Errorf("unexpected error for valid input: %v", err)
	}
}

func TestValidateImageUpload(t *testing.T) {
	if _, err := domain.ValidateImageUpload("application/pdf", 1000); err == nil {
		t.Error("expected an error for a disallowed content type")
	}
	if _, err := domain.ValidateImageUpload("image/jpeg", 6*1024*1024); err == nil {
		t.Error("expected an error for an oversized image")
	}
	if _, err := domain.ValidateImageUpload("image/jpeg", 0); err == nil {
		t.Error("expected an error for an empty image")
	}
	ext, err := domain.ValidateImageUpload("image/png", 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ext != ".png" {
		t.Errorf("expected extension .png, got %q", ext)
	}
}
