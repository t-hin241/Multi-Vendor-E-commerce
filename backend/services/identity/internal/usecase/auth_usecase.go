// Package usecase orchestrates Identity's account and session workflows.
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
	tx             Transactions
	cipher         *TokenCipher
	// verifications sends a new account its email link (PW-022); nil: none.
	verifications *EmailVerificationUseCase
}

// WithEmailVerification makes registration send the first email link.
func (uc *AuthUseCase) WithEmailVerification(v *EmailVerificationUseCase) *AuthUseCase {
	uc.verifications = v
	return uc
}

func NewAuthUseCase(
	users UserRepository,
	refreshTokens RefreshTokenRepository,
	passwordResets PasswordResetRepository,
	jwtManager *authjwt.Manager,
	log zerolog.Logger,
	tx Transactions, tokenCipher *TokenCipher,
) *AuthUseCase {
	return &AuthUseCase{
		users:          users,
		refreshTokens:  refreshTokens,
		passwordResets: passwordResets,
		jwtManager:     jwtManager,
		log:            log,
		tx:             tx, cipher: tokenCipher,
	}
}

type AuthResult struct {
	User                  *domain.User
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
}

// newUser validates a registration and hashes its password. It runs before
// the transaction: bcrypt takes about 100 ms of CPU, and holding a pooled
// connection and an open transaction for it starved session verification
// (every authenticated request of every service) during a login burst
// (load test, docs/module-details/17-observability-capacity.md).
func newUser(email, password, fullName, roleInput string) (*domain.User, error) {
	email = domain.NormalizeEmail(email)
	role := domain.Role(roleInput)

	if err := domain.ValidateRegistration(email, password, fullName, role); err != nil {
		return nil, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	return &domain.User{
		Email:        email,
		PasswordHash: string(passwordHash),
		FullName:     fullName,
		Role:         role,
	}, nil
}

// register stores a prepared user (newUser) and issues its first tokens.
func (uc *AuthUseCase) register(ctx context.Context, user *domain.User) (*AuthResult, error) {
	if err := uc.users.Create(ctx, user); err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			return nil, apperror.Conflict("Email is already registered")
		}
		return nil, apperror.Internal(err)
	}
	if err := uc.verifications.OnRegister(ctx, user); err != nil {
		return nil, err
	}

	return uc.issueTokens(ctx, user)
}

// checkCredentials finds the account and checks the password, outside any
// transaction (bcrypt holds no connection; see newUser).
func (uc *AuthUseCase) checkCredentials(ctx context.Context, email, password string) (*domain.User, error) {
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
	return user, nil
}

// login issues tokens for an account whose credentials were checked. It
// reads the account again inside the transaction: one deactivated or given
// a new password while the hash was being compared gets no session.
func (uc *AuthUseCase) login(ctx context.Context, checked *domain.User) (*AuthResult, error) {
	user, err := uc.users.FindByID(ctx, checked.ID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, apperror.Unauthorized("Invalid email or password")
		}
		return nil, apperror.Internal(err)
	}
	if !user.IsActive || user.PasswordHash != checked.PasswordHash {
		return nil, apperror.Unauthorized("Invalid email or password")
	}
	return uc.issueTokens(ctx, user)
}

func (uc *AuthUseCase) refreshToken(ctx context.Context, refreshTokenPlain string) (*AuthResult, error) {
	tokenHash := hashToken(refreshTokenPlain)

	stored, err := uc.refreshTokens.FindActiveByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, apperror.Unauthorized("Invalid refresh token")
		}
		return nil, apperror.Internal(err)
	}

	user, err := uc.users.FindByID(ctx, stored.UserID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !user.IsActive || stored.FamilyID == "" {
		return nil, apperror.Unauthorized("Invalid refresh token")
	}

	// Rotate: the presented token is single-use.
	if err := uc.refreshTokens.Revoke(ctx, stored.ID); err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, apperror.Unauthorized("Invalid refresh token")
		}
		return nil, apperror.Internal(err)
	}

	return uc.issueTokensForFamily(ctx, user, stored.FamilyID)
}

func (uc *AuthUseCase) Logout(ctx context.Context, token string) error {
	return uc.transaction(ctx, func(ctx context.Context) error {
		id, err := uc.refreshTokens.OwnerByHash(ctx, hashToken(token))
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = uc.users.FindByID(ctx, id); err != nil {
			return err
		}
		if err = uc.refreshTokens.RevokeByHash(ctx, hashToken(token)); err != nil {
			return err
		}
		return uc.users.Audit(ctx, id, id, "session_logout", "User logout")
	})
}

// requestPasswordReset queues delivery for active users and returns nil for
// unknown or inactive accounts. Infrastructure errors are returned to the caller.
func (uc *AuthUseCase) requestPasswordReset(ctx context.Context, email string) error {
	email = domain.NormalizeEmail(email)

	user, err := uc.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil
		}
		return apperror.Internal(err)
	}

	if !user.IsActive {
		return nil
	}
	if err := uc.passwordResets.InvalidateForUser(ctx, user.ID); err != nil {
		return apperror.Internal(err)
	}
	token, err := generateOpaqueToken()
	if err != nil {
		return apperror.Internal(err)
	}

	if err := uc.passwordResets.Create(ctx, user.ID, hashToken(token), time.Now().Add(passwordResetTTL)); err != nil {
		return apperror.Internal(err)
	}

	encrypted, err := uc.cipher.Encrypt(token, user.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	if err := uc.passwordResets.QueueDelivery(ctx, user.ID, hashToken(token), encrypted, time.Now().Add(passwordResetTTL)); err != nil {
		return apperror.Internal(err)
	}
	uc.log.Info().Str("event", "password_reset_queued").Msg("identity_security_event")

	return nil
}

func (uc *AuthUseCase) resetPassword(ctx context.Context, tokenPlain, newPassword string) error {
	if len(newPassword) < 8 || len(newPassword) > 72 {
		return apperror.Validation("Password must be 8 to 72 bytes")
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

	resetUser, err := uc.users.FindByID(ctx, stored.UserID)
	if err != nil {
		return apperror.Internal(err)
	}
	if !resetUser.IsActive {
		return apperror.Unauthorized("Invalid or expired reset token")
	}
	if err := uc.passwordResets.MarkUsed(ctx, stored.ID); err != nil {
		if errors.Is(err, repository.ErrPasswordResetTokenNotFound) {
			return apperror.Unauthorized("Invalid or expired reset token")
		}
		return apperror.Internal(err)
	}
	if err := uc.users.UpdatePasswordHash(ctx, stored.UserID, string(passwordHash)); err != nil {
		return apperror.Internal(err)
	}

	if err := uc.passwordResets.InvalidateForUser(ctx, stored.UserID); err != nil {
		return apperror.Internal(err)
	}

	if err := uc.refreshTokens.RevokeAllForUser(ctx, stored.UserID); err != nil {
		return apperror.Internal(err)
	}
	if err := uc.users.Audit(ctx, stored.UserID, stored.UserID, "password_reset", "Password reset completed"); err != nil {
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
	familyID, err := generateOpaqueToken()
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return uc.issueTokensForFamily(ctx, user, familyID)
}
func (uc *AuthUseCase) issueTokensForFamily(ctx context.Context, user *domain.User, familyID string) (*AuthResult, error) {
	accessToken, accessExpiresAt, err := uc.jwtManager.IssueSessionToken(user.ID, string(user.Role), familyID, accessTokenTTL)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	refreshToken, err := generateOpaqueToken()
	if err != nil {
		return nil, apperror.Internal(err)
	}
	refreshExpiresAt := time.Now().Add(refreshTokenTTL)

	if err := uc.refreshTokens.Create(ctx, user.ID, hashToken(refreshToken), refreshExpiresAt, familyID); err != nil {
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

func (uc *AuthUseCase) Register(ctx context.Context, email, password, fullName, roleInput string) (result *AuthResult, err error) {
	user, err := newUser(email, password, fullName, roleInput)
	if err != nil {
		return nil, err
	}
	err = uc.transaction(ctx, func(ctx context.Context) error {
		var e error
		result, e = uc.register(ctx, user)
		return e
	})
	return
}

func (uc *AuthUseCase) Login(ctx context.Context, email, password string) (result *AuthResult, err error) {
	user, err := uc.checkCredentials(ctx, email, password)
	if err != nil {
		return nil, err
	}
	err = uc.transaction(ctx, func(ctx context.Context) error { var e error; result, e = uc.login(ctx, user); return e })
	return
}

func (uc *AuthUseCase) RefreshToken(ctx context.Context, refreshTokenPlain string) (result *AuthResult, err error) {
	err = uc.transaction(ctx, func(ctx context.Context) error {
		var e error
		result, e = uc.refreshToken(ctx, refreshTokenPlain)
		return e
	})
	return
}

func (uc *AuthUseCase) RequestPasswordReset(ctx context.Context, email string) error {
	return uc.transaction(ctx, func(ctx context.Context) error { return uc.requestPasswordReset(ctx, email) })
}

func (uc *AuthUseCase) ResetPassword(ctx context.Context, tokenPlain, newPassword string) error {
	return uc.transaction(ctx, func(ctx context.Context) error { return uc.resetPassword(ctx, tokenPlain, newPassword) })
}
