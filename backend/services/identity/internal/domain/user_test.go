package domain_test

import (
	"testing"

	"shopee/backend/services/identity/internal/domain"
)

func TestValidateRegistration(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
		fullName string
		role     domain.Role
		wantErr  bool
	}{
		{"valid buyer", "a@example.com", "password123", "Alice", domain.RoleBuyer, false},
		{"valid vendor", "b@example.com", "password123", "Bob", domain.RoleVendor, false},
		{"invalid email", "not-an-email", "password123", "Alice", domain.RoleBuyer, true},
		{"short password", "a@example.com", "short", "Alice", domain.RoleBuyer, true},
		{"empty full name", "a@example.com", "password123", "  ", domain.RoleBuyer, true},
		{"admin role rejected", "a@example.com", "password123", "Alice", domain.RoleAdmin, true},
		{"unknown role rejected", "a@example.com", "password123", "Alice", domain.Role("superuser"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateRegistration(tt.email, tt.password, tt.fullName, tt.role)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRegistration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	got := domain.NormalizeEmail("  Alice@Example.COM  ")
	want := "alice@example.com"
	if got != want {
		t.Errorf("NormalizeEmail() = %q, want %q", got, want)
	}
}
