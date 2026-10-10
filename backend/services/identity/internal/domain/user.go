// Package domain defines Identity's users, roles and registration rules.
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

// SelfRegisterableRoles allows buyer and vendor roles during public registration.
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
	// PermissionVersion changes with every admin grant change (AF-19).
	PermissionVersion int64
	// EmailVerifiedAt (PW-022): when the account proved it owns Email.
	EmailVerifiedAt *time.Time
}

const minPasswordLength = 8

// ValidateRegistration validates email, password, full name and registration role.
func ValidateRegistration(email, password, fullName string, role Role) error {
	if strings.TrimSpace(fullName) == "" || len(fullName) > 200 {
		return apperror.Validation("Full name is required")
	}
	if parsed, err := mail.ParseAddress(email); err != nil || len(email) > 254 || parsed.Address != email {
		return apperror.Validation("A valid email address is required")
	}
	if len(password) < minPasswordLength || len(password) > 72 {
		return apperror.Validation("Password must be 8 to 72 bytes")
	}
	if !SelfRegisterableRoles[role] {
		return apperror.Validation("Role must be buyer or vendor")
	}
	return nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
