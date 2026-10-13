package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

type AccountStore interface {
	UpdateProfile(context.Context, string, domain.ProfileUpdate) error
	Sessions(context.Context, string, time.Time, string, int) ([]domain.AccountSession, error)
	EmailChangeRate(context.Context, string) (time.Time, int, error)
	IssueEmailChange(context.Context, string, string, []byte, []byte, int64, time.Time) (string, error)
	EmailChange(context.Context, string) (*domain.EmailChange, error)
	CompleteEmailChange(context.Context, *domain.EmailChange, string, []byte) error
	InvalidateEmailChanges(context.Context, string) error
}
type AccountActor struct{ UserID, SessionID, Role string }
type AccountUseCase struct {
	Store     AccountStore
	Users     UserRepository
	Refresh   RefreshTokenRepository
	Resets    PasswordResetRepository
	Tx        Transactions
	Cipher    *TokenCipher
	Enabled   bool
	VerifyTTL time.Duration
}

var errAccountOff = &apperror.Error{Code: "feature_disabled", Status: 404, Message: "Account self-service is not enabled"}
var errEmailChange = &apperror.Error{Code: "invalid_email_change", Status: 400, Message: "Unable to change email. Check the request or ask for a new link."}

func (u *AccountUseCase) actor(ctx context.Context, a AccountActor) (*domain.User, error) {
	if !u.Enabled {
		return nil, errAccountOff
	}
	user, err := u.Users.FindByID(ctx, a.UserID)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, apperror.Unauthorized("Please sign in again")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !user.IsActive || a.SessionID == "" || string(user.Role) != a.Role {
		return nil, apperror.Unauthorized("Please sign in again")
	}
	active, err := u.Refresh.SessionActive(ctx, user.ID, a.Role, a.SessionID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !active {
		return nil, apperror.Unauthorized("Please sign in again")
	}
	return user, nil
}

func (u *AccountUseCase) Profile(ctx context.Context, a AccountActor, p domain.ProfileUpdate) (result *domain.User, err error) {
	if !u.Enabled {
		return nil, errAccountOff
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	err = u.Tx.Run(ctx, func(ctx context.Context) error {
		if _, e := u.actor(ctx, a); e != nil {
			return e
		}
		if e := u.Store.UpdateProfile(ctx, a.UserID, p); e != nil {
			if errors.Is(e, repository.ErrProfileConflict) {
				return apperror.Conflict("Profile changed; reload before saving")
			}
			return apperror.Internal(e)
		}
		if e := u.Users.Audit(ctx, a.UserID, a.UserID, "profile_updated", "Self-service profile update"); e != nil {
			return apperror.Internal(e)
		}
		var e error
		result, e = u.Users.FindByID(ctx, a.UserID)
		return e
	})
	return
}

func (u *AccountUseCase) checkedPassword(ctx context.Context, a AccountActor, password string) (*domain.User, error) {
	user, err := u.actor(ctx, a)
	if err != nil {
		return nil, err
	}
	if len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, domain.AccountValidation("current_password: password is incorrect")
	}
	return user, nil
}

func (u *AccountUseCase) ChangePassword(ctx context.Context, a AccountActor, current, next string) error {
	if !u.Enabled {
		return errAccountOff
	}
	if len(next) < 8 || len(next) > 72 {
		return domain.AccountValidation("new_password: must be 8 to 72 bytes")
	}
	checked, err := u.checkedPassword(ctx, a, current)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcryptCost)
	if err != nil {
		return apperror.Internal(err)
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		user, err := u.actor(ctx, a)
		if err != nil {
			return err
		}
		if user.PasswordHash != checked.PasswordHash || user.AuthVersion != checked.AuthVersion {
			return apperror.Conflict("Account changed; sign in again")
		}
		if err = u.Users.UpdatePasswordHash(ctx, user.ID, string(hash)); err != nil {
			return apperror.Internal(err)
		}
		if err = u.Store.InvalidateEmailChanges(ctx, user.ID); err != nil {
			return apperror.Internal(err)
		}
		if err = u.Resets.InvalidateForUser(ctx, user.ID); err != nil {
			return apperror.Internal(err)
		}
		if err = u.Refresh.RevokeAllForUser(ctx, user.ID); err != nil {
			return apperror.Internal(err)
		}
		return u.Users.Audit(ctx, user.ID, user.ID, "password_changed", "Self-service password change; all sessions revoked")
	})
}

type sessionCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func (u *AccountUseCase) Sessions(ctx context.Context, a AccountActor, cursor string, limit int) (*domain.SessionPage, error) {
	if _, err := u.actor(ctx, a); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, domain.AccountValidation("limit: must be between 1 and 100")
	}
	var c sessionCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 256 || json.Unmarshal(raw, &c) != nil || c.At.IsZero() || c.ID == "" {
			return nil, domain.AccountValidation("cursor: invalid cursor")
		}
	}
	items, err := u.Store.Sessions(ctx, a.UserID, c.At, c.ID, limit+1)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	page := &domain.SessionPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		raw, err := json.Marshal(sessionCursor{last.CreatedAt, last.ID})
		if err != nil {
			return nil, apperror.Internal(err)
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for i := range page.Items {
		page.Items[i].Current = page.Items[i].ID == a.SessionID
	}
	return page, nil
}

func (u *AccountUseCase) Revoke(ctx context.Context, a AccountActor, sessionID string, all bool) error {
	if sessionID == "" && !all {
		return domain.AccountValidation("session id is required")
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		user, err := u.actor(ctx, a)
		if err != nil {
			return err
		}
		if all {
			err = u.Refresh.RevokeAllForUser(ctx, user.ID)
		} else {
			err = u.Refresh.RevokeSession(ctx, user.ID, sessionID)
		}
		if err != nil {
			return apperror.Internal(err)
		}
		return u.Users.Audit(ctx, user.ID, user.ID, "self_sessions_revoked", "Self-service session revocation")
	})
}

// Sensitive delivery material is encrypted at Identity. Notification receives
// only a delivery reference, fetching this payload during its lease.
type accountEmailMaterial struct {
	Email string `json:"email"`
	Token string `json:"token,omitempty"`
}

func (u *AccountUseCase) seal(userID, email, token string) ([]byte, error) {
	raw, err := json.Marshal(accountEmailMaterial{email, token})
	if err != nil {
		return nil, err
	}
	return u.Cipher.Encrypt(string(raw), userID)
}
func (u *AccountUseCase) RequestEmailChange(ctx context.Context, a AccountActor, email, password string) error {
	if !u.Enabled {
		return errAccountOff
	}
	email = domain.NormalizeEmail(email)
	if parsed, err := mail.ParseAddress(email); err != nil || len(email) > 254 || parsed.Address != email {
		return domain.AccountValidation("new_email: enter a valid email address")
	}
	checked, err := u.checkedPassword(ctx, a, password)
	if err != nil {
		return err
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		user, err := u.actor(ctx, a)
		if err != nil {
			return err
		}
		if user.PasswordHash != checked.PasswordHash || user.AuthVersion != checked.AuthVersion {
			return apperror.Conflict("Account changed; sign in again")
		}
		if domain.NormalizeEmail(user.Email) == email {
			return errEmailChange
		}
		last, count, err := u.Store.EmailChangeRate(ctx, user.ID)
		if err != nil {
			return apperror.Internal(err)
		}
		if time.Since(last) < time.Minute || count >= 5 {
			return errTooSoon
		}
		token, err := generateOpaqueToken()
		if err != nil {
			return apperror.Internal(err)
		}
		target, err := u.Cipher.Encrypt(email, user.ID)
		if err != nil {
			return apperror.Internal(err)
		}
		delivery, err := u.seal(user.ID, email, token)
		if err != nil {
			return apperror.Internal(err)
		}
		ttl := u.VerifyTTL
		if ttl == 0 {
			ttl = 30 * time.Minute
		}
		if _, err = u.Store.IssueEmailChange(ctx, user.ID, hashToken(token), target, delivery, user.AuthVersion, time.Now().Add(ttl)); err != nil {
			return apperror.Internal(err)
		}
		return u.Users.Audit(ctx, user.ID, user.ID, "email_change_requested", "New address confirmation queued")
	})
}

func (u *AccountUseCase) ConfirmEmailChange(ctx context.Context, token string) error {
	if !u.Enabled {
		return errAccountOff
	}
	if len(token) < 20 || len(token) > 200 {
		return errEmailChange
	}
	return u.Tx.Run(ctx, func(ctx context.Context) error {
		op, err := u.Store.EmailChange(ctx, hashToken(token))
		if errors.Is(err, repository.ErrEmailChangeInvalid) {
			return errEmailChange
		}
		if err != nil {
			return apperror.Internal(err)
		}
		user, err := u.Users.FindByID(ctx, op.UserID)
		if err != nil {
			return apperror.Internal(err)
		}
		// Re-read after taking the user lock, so replacement, reset and concurrent
		// confirmation cannot consume stale material.
		op, err = u.Store.EmailChange(ctx, hashToken(token))
		if err != nil {
			return apperror.Internal(err)
		}
		if !user.IsActive || op.UsedAt != nil || !time.Now().Before(op.ExpiresAt) || op.AuthVersion != user.AuthVersion {
			return errEmailChange
		}
		email, err := u.Cipher.Decrypt(op.TargetCiphertext, user.ID)
		if err != nil {
			return apperror.Internal(err)
		}
		notice, err := u.seal(user.ID, user.Email, "")
		if err != nil {
			return apperror.Internal(err)
		}
		if err = u.Store.CompleteEmailChange(ctx, op, email, notice); errors.Is(err, repository.ErrEmailChangeInvalid) {
			return errEmailChange
		} else if err != nil {
			return apperror.Internal(err)
		}
		if err = u.Resets.InvalidateForUser(ctx, user.ID); err != nil {
			return apperror.Internal(err)
		}
		if err = u.Refresh.RevokeAllForUser(ctx, user.ID); err != nil {
			return apperror.Internal(err)
		}
		return u.Users.Audit(ctx, user.ID, user.ID, "email_changed", "Address confirmed; all sessions revoked; previous address notified")
	})
}
