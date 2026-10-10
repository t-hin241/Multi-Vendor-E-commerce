package usecase

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// PW-022: an account proves it owns its email address with a one-time link
// sent like a password reset link. Vendor accepts shop invitations only
// from verified addresses once FEATURE_STAFF_REQUIRES_VERIFIED_EMAIL is on.

const (
	emailVerificationTTL  = 48 * time.Hour
	emailVerificationWait = time.Minute
)

// EmailVerificationStore is repository.EmailVerificationRepository.
type EmailVerificationStore interface {
	Issue(ctx context.Context, userID, email, hash string, encrypted []byte, expires time.Time) error
	LastIssued(ctx context.Context, userID string) (time.Time, error)
	Use(ctx context.Context, hash string) (string, error)
}

type EmailVerificationUseCase struct {
	Store  EmailVerificationStore
	Users  UserRepository
	Tx     Transactions
	Cipher *TokenCipher
	// Enabled is FEATURE_EMAIL_VERIFICATION_ENABLED: off sends no link and
	// refuses the endpoints (nothing is required of accounts either).
	Enabled bool
	Log     zerolog.Logger
}

var (
	errVerificationOff = &apperror.Error{Code: "feature_disabled", Status: http.StatusNotFound, Message: "Email verification is not enabled"}
	errAlreadyVerified = &apperror.Error{Code: "already_verified", Status: http.StatusConflict, Message: "This email address is already verified"}
	errTooSoon         = &apperror.Error{Code: "rate_limited", Status: http.StatusTooManyRequests, Message: "A link was sent a moment ago; check your inbox or try again in a minute"}
	errLinkInvalid     = &apperror.Error{Code: "invalid_token", Status: http.StatusBadRequest, Message: "This link is invalid or has expired; ask for a new one"}
)

// issue creates a link for the user's current address, in the caller's
// transaction.
func (u *EmailVerificationUseCase) issue(ctx context.Context, user *domain.User) error {
	token, err := generateOpaqueToken()
	if err != nil {
		return apperror.Internal(err)
	}
	encrypted, err := u.Cipher.Encrypt(token, user.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	if err := u.Store.Issue(ctx, user.ID, user.Email, hashToken(token), encrypted, time.Now().Add(emailVerificationTTL)); err != nil {
		return apperror.Internal(err)
	}
	u.Log.Info().Str("event", "email_verification_queued").Msg("identity_security_event")
	return nil
}

// OnRegister sends the first link to a new account (registration's
// transaction); nothing with the feature off.
func (u *EmailVerificationUseCase) OnRegister(ctx context.Context, user *domain.User) error {
	if u == nil || !u.Enabled {
		return nil
	}
	return u.issue(ctx, user)
}

// Resend sends a new link to the signed-in account (one a minute).
func (u *EmailVerificationUseCase) Resend(ctx context.Context, userID string) error {
	if !u.Enabled {
		return errVerificationOff
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		user, err := u.Users.FindByID(ctx, userID)
		if err != nil {
			return apperror.Internal(err)
		}
		if user.EmailVerifiedAt != nil {
			return errAlreadyVerified
		}
		last, err := u.Store.LastIssued(ctx, user.ID)
		if err != nil {
			return apperror.Internal(err)
		}
		if time.Since(last) < emailVerificationWait {
			return errTooSoon
		}
		return u.issue(ctx, user)
	})
}

// Confirm consumes a link; the token is the proof, no session is needed.
func (u *EmailVerificationUseCase) Confirm(ctx context.Context, token string) error {
	if !u.Enabled {
		return errVerificationOff
	}
	if len(token) < 20 || len(token) > 200 {
		return errLinkInvalid
	}
	err := u.Tx.Run(ctx, func(ctx context.Context) error {
		userID, err := u.Store.Use(ctx, hashToken(token))
		if errors.Is(err, repository.ErrVerificationNotFound) {
			return errLinkInvalid
		}
		if err != nil {
			return apperror.Internal(err)
		}
		u.Log.Info().Str("event", "email_verified").Str("user_id", userID).Msg("identity_security_event")
		return nil
	})
	return err
}
