package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// AdminUseCase covers admin's trust-and-safety actions on user accounts —
// separate from AuthUseCase, which owns the buyer/vendor/admin-self-service
// auth flows (register/login/refresh/password-reset) rather than another
// admin's moderation of accounts.
type AdminUseCase struct {
	users         UserRepository
	refreshTokens RefreshTokenRepository
}

func NewAdminUseCase(users UserRepository, refreshTokens RefreshTokenRepository) *AdminUseCase {
	return &AdminUseCase{users: users, refreshTokens: refreshTokens}
}

var validRoleFilters = map[string]bool{"": true, "buyer": true, "vendor": true, "admin": true}

func (uc *AdminUseCase) ListUsers(ctx context.Context, role, q string, limit, offset int) ([]*domain.User, error) {
	if !validRoleFilters[role] {
		return nil, apperror.Validation("Invalid role filter")
	}

	users, err := uc.users.List(ctx, role, q, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return users, nil
}

// SetActive suspends or reactivates a user's account. An admin account can
// never be the target here — blocks both self-lockout and one admin
// disabling another, since there's no separate "is this actually me" check
// needed once the target's role is admin. Deactivating also revokes every
// refresh token the user currently holds, so an already-issued session dies
// immediately instead of merely being blocked on its next login — the same
// "invalidate existing sessions on a state-changing action" idiom
// ResetPassword already uses.
func (uc *AdminUseCase) SetActive(ctx context.Context, targetUserID string, isActive bool) (*domain.User, error) {
	target, err := uc.users.FindByID(ctx, targetUserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperror.NotFound("User not found")
		}
		return nil, apperror.Internal(err)
	}

	if target.Role == domain.RoleAdmin {
		return nil, apperror.Forbidden("Cannot change another admin's account status")
	}

	if err := uc.users.SetActive(ctx, targetUserID, isActive); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperror.NotFound("User not found")
		}
		return nil, apperror.Internal(err)
	}
	target.IsActive = isActive

	if !isActive {
		if err := uc.refreshTokens.RevokeAllForUser(ctx, targetUserID); err != nil {
			return nil, apperror.Internal(err)
		}
	}

	return target, nil
}
