package usecase

import (
	"context"
	"errors"
	"fmt"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// AdminUseCase lists accounts, changes account status and revokes sessions for admins.
type AdminUseCase struct {
	users         UserRepository
	refreshTokens RefreshTokenRepository
	tx            Transactions
}

func NewAdminUseCase(users UserRepository, refreshTokens RefreshTokenRepository, tx Transactions) *AdminUseCase {
	return &AdminUseCase{users: users, refreshTokens: refreshTokens, tx: tx}
}

var validRoleFilters = map[string]bool{"": true, "buyer": true, "vendor": true, "admin": true}

func (uc *AdminUseCase) ListUsers(ctx context.Context, actorID, role, q string, limit, offset int) ([]*domain.User, error) {
	if err := uc.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, apperror.Validation("Invalid pagination")
	}
	if !validRoleFilters[role] {
		return nil, apperror.Validation("Invalid role filter")
	}

	users, err := uc.users.List(ctx, role, q, limit, offset)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return users, nil
}

// setActive changes a non-admin account's status, revokes sessions when disabling
// the account and records the action in the audit log.
func (uc *AdminUseCase) setActive(ctx context.Context, actorID, targetUserID string, isActive bool) (*domain.User, error) {
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

	if err := uc.users.Audit(ctx, actorID, targetUserID, "account_status_changed", fmt.Sprintf("is_active=%t", isActive)); err != nil {
		return nil, apperror.Internal(err)
	}
	return target, nil
}

func (uc *AdminUseCase) requireAdmin(ctx context.Context, id string) error {
	user, err := uc.users.FindByID(ctx, id)
	if errors.Is(err, repository.ErrUserNotFound) {
		return apperror.Forbidden("Admin access required")
	}
	if err != nil {
		return apperror.Internal(err)
	}
	if !user.IsActive || user.Role != domain.RoleAdmin {
		return apperror.Forbidden("Admin access required")
	}
	return nil
}
func (uc *AdminUseCase) SetActive(ctx context.Context, actorID, targetID string, active bool) (result *domain.User, err error) {
	err = uc.tx.Run(ctx, func(ctx context.Context) error {
		if e := uc.requireAdmin(ctx, actorID); e != nil {
			return e
		}
		var e error
		result, e = uc.setActive(ctx, actorID, targetID, active)
		return e
	})
	if err != nil {
		var app *apperror.Error
		if !errors.As(err, &app) {
			err = apperror.Internal(err)
		}
	}
	return
}
func (uc *AdminUseCase) RevokeSession(ctx context.Context, actorID, userID, sessionID string) error {
	err := uc.tx.Run(ctx, func(ctx context.Context) error {
		if e := uc.requireAdmin(ctx, actorID); e != nil {
			return e
		}
		if _, e := uc.users.FindByID(ctx, userID); e != nil {
			return e
		}
		if e := uc.refreshTokens.RevokeSession(ctx, userID, sessionID); e != nil {
			return e
		}
		return uc.users.Audit(ctx, actorID, userID, "session_revoked", "Administrative session revocation")
	})
	if err != nil {
		var app *apperror.Error
		if !errors.As(err, &app) {
			return apperror.Internal(err)
		}
	}
	return err
}
