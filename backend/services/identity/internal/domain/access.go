package domain

import (
	"regexp"
	"strings"
	"time"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
)

const (
	GrantActive  = "active"
	GrantRevoked = "revoked"

	// ProofTTL is how long a reauthentication proof can be used.
	ProofTTL = 5 * time.Minute
)

// PermissionGrant gives one admin one permission bundle (AF-19).
type PermissionGrant struct {
	ID           string
	UserID       string
	Bundle       string
	Status       string
	GrantedBy    *string
	Reason       string
	CreatedAt    time.Time
	RevokedBy    *string
	RevokeReason *string
	RevokedAt    *time.Time
}

// ValidateGrant checks a grant request.
func ValidateGrant(bundle, reason string) (string, error) {
	if !adminaccess.Known(bundle) {
		return "", apperror.Validation("Unknown permission bundle")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return "", apperror.Validation("A reason of 1-500 characters is required")
	}
	return reason, nil
}

var purposeFormat = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{2,63}$`)

// ValidateProofRequest checks the purpose and operation a proof binds to.
func ValidateProofRequest(purpose, operationHash string) error {
	if !purposeFormat.MatchString(purpose) {
		return apperror.Validation("Invalid reauthentication purpose")
	}
	if n := len(operationHash); n == 0 || n > 200 || strings.ContainsAny(operationHash, "\r\n") {
		return apperror.Validation("Invalid operation reference")
	}
	return nil
}
