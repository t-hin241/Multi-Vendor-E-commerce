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

// TOTPStore is repository.TOTPRepository.
type TOTPStore interface {
	Find(ctx context.Context, userID string) (*repository.TOTP, error)
	Start(ctx context.Context, userID string, ciphertext []byte) error
	Confirm(ctx context.Context, userID string, step int64, codeHashes []string) error
	UseStep(ctx context.Context, userID string, step int64) error
	UseRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error)
	RecoveryCodesLeft(ctx context.Context, userID string) (int, error)
}

// TOTPUseCase runs PW-028: an admin enrolls an authenticator app, and
// reauthentication for money operations needs its code (or a recovery
// code) when FEATURE_ADMIN_MFA_REQUIRED is on.
type TOTPUseCase struct {
	Store TOTPStore
	Users UserRepository
	Tx    Transactions
	// Cipher seals secrets (IDENTITY_MFA_ENCRYPTION_KEY); nil turns
	// enrollment off.
	Cipher *TokenCipher
	Issuer string
	Now    func() time.Time
	Log    zerolog.Logger
}

var (
	errMFAOff          = &apperror.Error{Code: "feature_disabled", Status: http.StatusNotFound, Message: "Two-step verification is not configured"}
	errMFAEnrolled     = &apperror.Error{Code: "mfa_already_enrolled", Status: http.StatusConflict, Message: "An authenticator is already set up for this account"}
	errMFANotStarted   = &apperror.Error{Code: "mfa_not_started", Status: http.StatusConflict, Message: "Start setting up the authenticator first"}
	errMFACodeInvalid  = &apperror.Error{Code: "mfa_code_invalid", Status: http.StatusUnauthorized, Message: "The verification code is incorrect or was already used"}
	errMFACodeRequired = &apperror.Error{Code: "mfa_code_required", Status: http.StatusUnauthorized, Message: "Enter the code from your authenticator app"}
	errMFAEnrollment   = &apperror.Error{Code: "mfa_enrollment_required", Status: http.StatusForbidden, Message: "Set up an authenticator app before this operation"}
)

func (u *TOTPUseCase) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func totpAAD(userID string) string { return "admin_totp:" + userID }

// TOTPEnrollment is what the admin's app imports, shown once.
type TOTPEnrollment struct {
	Secret string
	URI    string
}

// TOTPStatus is whether the admin has a confirmed authenticator.
type TOTPStatus struct {
	Enrolled           bool
	RecoveryCodesLeft  int
	EnrollmentPossible bool
}

func (u *TOTPUseCase) admin(ctx context.Context, userID string) (*domain.User, error) {
	user, err := u.Users.FindByID(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !user.IsActive || user.Role != domain.RoleAdmin {
		return nil, apperror.Forbidden("Admin access required")
	}
	return user, nil
}

// Status of the signed-in admin.
func (u *TOTPUseCase) Status(ctx context.Context, userID string) (*TOTPStatus, error) {
	if _, err := u.admin(ctx, userID); err != nil {
		return nil, err
	}
	out := &TOTPStatus{EnrollmentPossible: u.Cipher != nil}
	t, err := u.Store.Find(ctx, userID)
	if errors.Is(err, repository.ErrTOTPNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	out.Enrolled = t.ConfirmedAt != nil
	if out.Enrolled {
		if out.RecoveryCodesLeft, err = u.Store.RecoveryCodesLeft(ctx, userID); err != nil {
			return nil, apperror.Internal(err)
		}
	}
	return out, nil
}

// Start creates a new secret for the admin's app (not active until a code
// from the app confirms it).
func (u *TOTPUseCase) Start(ctx context.Context, userID string) (*TOTPEnrollment, error) {
	if u.Cipher == nil {
		return nil, errMFAOff
	}
	user, err := u.admin(ctx, userID)
	if err != nil {
		return nil, err
	}
	secret, err := domain.NewTOTPSecret()
	if err != nil {
		return nil, apperror.Internal(err)
	}
	sealed, err := u.Cipher.Encrypt(domain.TOTPSecretText(secret), totpAAD(userID))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if err := u.Store.Start(ctx, userID, sealed); errors.Is(err, repository.ErrStaleState) {
		return nil, errMFAEnrolled
	} else if err != nil {
		return nil, apperror.Internal(err)
	}
	return &TOTPEnrollment{Secret: domain.TOTPSecretText(secret), URI: domain.TOTPURI(u.Issuer, user.Email, secret)}, nil
}

// Confirm activates the authenticator with a code from the app and returns
// the recovery codes, shown once.
func (u *TOTPUseCase) Confirm(ctx context.Context, userID, code string) ([]string, error) {
	if u.Cipher == nil {
		return nil, errMFAOff
	}
	if _, err := u.admin(ctx, userID); err != nil {
		return nil, err
	}
	var codes []string
	err := u.Tx.Run(ctx, func(ctx context.Context) error {
		t, err := u.Store.Find(ctx, userID)
		if errors.Is(err, repository.ErrTOTPNotFound) {
			return errMFANotStarted
		}
		if err != nil {
			return apperror.Internal(err)
		}
		if t.ConfirmedAt != nil {
			return errMFAEnrolled
		}
		secret, err := u.open(t)
		if err != nil {
			return err
		}
		step, ok := domain.VerifyTOTP(secret, code, u.now(), t.LastUsedStep)
		if !ok {
			return errMFACodeInvalid
		}
		hashes := make([]string, 0, domain.RecoveryCodeCount)
		for range domain.RecoveryCodeCount {
			c, err := domain.NewRecoveryCode()
			if err != nil {
				return apperror.Internal(err)
			}
			codes = append(codes, c)
			hashes = append(hashes, hashToken(c))
		}
		if err := u.Store.Confirm(ctx, userID, step, hashes); err != nil {
			return apperror.Internal(err)
		}
		return u.Users.Audit(ctx, userID, userID, "mfa_enrolled", "totp")
	})
	if err != nil {
		return nil, err
	}
	u.Log.Info().Str("event", "mfa_enrolled").Str("user_id", userID).Msg("identity_security_event")
	return codes, nil
}

func (u *TOTPUseCase) open(t *repository.TOTP) ([]byte, error) {
	text, err := u.Cipher.Decrypt(t.Ciphertext, totpAAD(t.UserID))
	if err != nil {
		return nil, apperror.Internal(errors.New("authenticator secret unreadable"))
	}
	secret, err := domain.TOTPSecretBytes(text)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return secret, nil
}

// Require checks the second factor of a reauthentication (a code from the
// app, or an unused recovery code), in its own transaction.
func (u *TOTPUseCase) Require(ctx context.Context, userID, code string) error {
	if u.Cipher == nil {
		return errMFAEnrollment
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		t, err := u.Store.Find(ctx, userID)
		if errors.Is(err, repository.ErrTOTPNotFound) || (err == nil && t.ConfirmedAt == nil) {
			return errMFAEnrollment
		}
		if err != nil {
			return apperror.Internal(err)
		}
		if code == "" {
			return errMFACodeRequired
		}
		if domain.IsRecoveryCode(code) {
			ok, err := u.Store.UseRecoveryCode(ctx, userID, hashToken(code))
			if err != nil {
				return apperror.Internal(err)
			}
			if !ok {
				return errMFACodeInvalid
			}
			return u.Users.Audit(ctx, userID, userID, "mfa_recovery_code_used", "totp")
		}
		secret, err := u.open(t)
		if err != nil {
			return err
		}
		step, ok := domain.VerifyTOTP(secret, code, u.now(), t.LastUsedStep)
		if !ok {
			return errMFACodeInvalid
		}
		if err := u.Store.UseStep(ctx, userID, step); err != nil {
			return apperror.Internal(err)
		}
		return nil
	})
}
