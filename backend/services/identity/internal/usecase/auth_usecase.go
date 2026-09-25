// Package usecase orchestrates Identity's auth workflows: registration,
// login, refresh/rotation, logout and password reset. It owns the
// transaction boundary and never lets a repository or transport concern
// leak into the domain rules.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

const (
	accessTokenTTL   = 15 * time.Minute
	refreshTokenTTL  = 30 * 24 * time.Hour
	passwordResetTTL = 1 * time.Hour
	bcryptCost       = 12
)

type AuthUseCase struct {
	users          UserRepository
	refreshTokens  RefreshTokenRepository
	passwordResets PasswordResetRepository
	jwtManager     *authjwt.Manager
	log            zerolog.Logger
	isDevelopment  bool
}

func NewAuthUseCase(
	users UserRepository,
	refreshTokens RefreshTokenRepository,
	passwordResets PasswordResetRepository,
	jwtManager *authjwt.Manager,
	log zerolog.Logger,
	env string,
) *AuthUseCase {
	return &AuthUseCase{
		users:          users,
		refreshTokens:  refreshTokens,
		passwordResets: passwordResets,
		jwtManager:     jwtManager,
		log:            log,
		isDevelopment:  env != "production",
	}
}

type AuthResult struct {
	User                  *domain.User
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

func (uc *AuthUseCase) Register(ctx context.Context, email, password, fullName, roleInput string) (*AuthResult, error) {
	email = domain.NormalizeEmail(email)
	role := domain.Role(roleInput)

	if err := domain.ValidateRegistration(email, password, fullName, role); err != nil {
		return nil, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	user := &domain.User{
		Email:        email,
		PasswordHash: string(passwordHash),
		FullName:     fullName,
		Role:         role,
	}

	if err := uc.users.Create(ctx, user); err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			return nil, apperror.Conflict("Email is already registered")
		}
		return nil, apperror.Internal(err)
	}

	return uc.issueTokens(ctx, user)
}

func (uc *AuthUseCase) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	email = domain.NormalizeEmail(email)

	user, err := uc.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperror.Unauthorized("Invalid email or password")
		}
		return nil, apperror.Internal(err)
	}

	if !user.IsActive {
		return nil, apperror.Unauthorized("Invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, apperror.Unauthorized("Invalid email or password")
	}

	return uc.issueTokens(ctx, user)
}

func (uc *AuthUseCase) RefreshToken(ctx context.Context, refreshTokenPlain string) (*AuthResult, error) {
	tokenHash := hashToken(refreshTokenPlain)

	stored, err := uc.refreshTokens.FindActiveByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, apperror.Unauthorized("Invalid refresh token")
		}
		return nil, apperror.Internal(err)
	}

	user, err := uc.users.FindByID(ctx, stored.UserID)
	if err != nil || !user.IsActive {
		return nil, apperror.Unauthorized("Invalid refresh token")
	}

	// Rotate: the presented token is single-use.
	if err := uc.refreshTokens.Revoke(ctx, stored.ID); err != nil {
		return nil, apperror.Internal(err)
	}

	return uc.issueTokens(ctx, user)
}

func (uc *AuthUseCase) Logout(ctx context.Context, refreshTokenPlain string) error {
	if err := uc.refreshTokens.RevokeByHash(ctx, hashToken(refreshTokenPlain)); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// RequestPasswordReset always succeeds from the caller's perspective,
// whether or not the email is registered, so the endpoint can't be used to
// enumerate accounts.
func (uc *AuthUseCase) RequestPasswordReset(ctx context.Context, email string) error {
	email = domain.NormalizeEmail(email)

	user, err := uc.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil
		}
		return apperror.Internal(err)
	}

	token, err := generateOpaqueToken()
	if err != nil {
		return apperror.Internal(err)
	}

	if err := uc.passwordResets.Create(ctx, user.ID, hashToken(token), time.Now().Add(passwordResetTTL)); err != nil {
		return apperror.Internal(err)
	}

	// Delivery goes through the Notification service once it consumes a
	// PasswordResetRequested event; until then, surface the token in dev
	// logs only so the flow is testable locally without a mailer.
	if uc.isDevelopment {
		uc.log.Info().Str("user_id", user.ID).Str("reset_token", token).Msg("password_reset_requested_dev_only")
	}

	return nil
}

func (uc *AuthUseCase) ResetPassword(ctx context.Context, tokenPlain, newPassword string) error {
	if len(newPassword) < 8 {
		return apperror.Validation("Password must be at least 8 characters")
	}

	stored, err := uc.passwordResets.FindUsableByHash(ctx, hashToken(tokenPlain))
	if err != nil {
		if errors.Is(err, repository.ErrPasswordResetTokenNotFound) {
			return apperror.Unauthorized("Invalid or expired reset token")
		}
		return apperror.Internal(err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return apperror.Internal(err)
	}

	if err := uc.users.UpdatePasswordHash(ctx, stored.UserID, string(passwordHash)); err != nil {
		return apperror.Internal(err)
	}

	if err := uc.passwordResets.MarkUsed(ctx, stored.ID); err != nil {
		return apperror.Internal(err)
	}

	if err := uc.refreshTokens.RevokeAllForUser(ctx, stored.UserID); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

func (uc *AuthUseCase) Me(ctx context.Context, userID string) (*domain.User, error) {
	user, err := uc.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperror.NotFound("User not found")
		}
		return nil, apperror.Internal(err)
	}
	return user, nil
}

func (uc *AuthUseCase) issueTokens(ctx context.Context, user *domain.User) (*AuthResult, error) {
	accessToken, accessExpiresAt, err := uc.jwtManager.IssueAccessToken(user.ID, string(user.Role), accessTokenTTL)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	refreshToken, err := generateOpaqueToken()
	if err != nil {
		return nil, apperror.Internal(err)
	}
	refreshExpiresAt := time.Now().Add(refreshTokenTTL)

	if err := uc.refreshTokens.Create(ctx, user.ID, hashToken(refreshToken), refreshExpiresAt); err != nil {
		return nil, apperror.Internal(err)
	}

	return &AuthResult{
		User:                  user,
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshExpiresAt,
	}, nil
}
