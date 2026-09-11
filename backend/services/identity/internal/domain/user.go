// Package domain holds Identity's entities and business rules. It has no
// dependency on HTTP, the database driver or any provider SDK.
package domain

import (
	"net/mail"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type Role string

const (
	RoleBuyer  Role = "buyer"
	RoleVendor Role = "vendor"
	RoleAdmin  Role = "admin"
)

// SelfRegisterableRoles are the roles a user may pick for themselves through
// public registration. Admin accounts are never created this way.
var SelfRegisterableRoles = map[Role]bool{
	RoleBuyer:  true,
	RoleVendor: true,
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
	FullName     string
	Role         Role
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const minPasswordLength = 8

// ValidateRegistration checks the fields a new user must supply, independent
// of any persistence concern (email uniqueness is checked by the use case,
// which owns the database round trip).
func ValidateRegistration(email, password, fullName string, role Role) error {
	if strings.TrimSpace(fullName) == "" {
		return apperror.Validation("Full name is required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return apperror.Validation("A valid email address is required")
	}
	if len(password) < minPasswordLength {
		return apperror.Validation("Password must be at least 8 characters")
	}
	if !SelfRegisterableRoles[role] {
		return apperror.Validation("Role must be buyer or vendor")
	}
	return nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
