package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrVerificationNotFound: no usable link with this token.
var ErrVerificationNotFound = errors.New("repository: email verification not found")

// EmailVerificationRepository stores PW-022 links and queues their
// delivery in the shared delivery queue (kind email_verification).
type EmailVerificationRepository struct{ Pool *pgxpool.Pool }

// EmailVerification is one issued link.
type EmailVerification struct {
	ID, UserID, Email string
	ExpiresAt         time.Time
	UsedAt            *time.Time
	CreatedAt         time.Time
}

// Issue stores the hashed token and queues its encrypted copy, in the
// caller's transaction; earlier unused links of the user stop working.
func (r EmailVerificationRepository) Issue(ctx context.Context, userID, email, hash string, encrypted []byte, expires time.Time) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `UPDATE email_verification_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE password_reset_deliveries SET encrypted_token = NULL, status = 'expired', lease_until = NULL
		WHERE user_id = $1 AND kind = 'email_verification' AND encrypted_token IS NOT NULL`, userID); err != nil {
		return err
	}
	var id string
	if err := q.QueryRow(ctx, `INSERT INTO email_verification_tokens (user_id, email, token_hash, expires_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, email, hash, expires).Scan(&id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `INSERT INTO password_reset_deliveries (kind, verification_id, user_id, encrypted_token, expires_at)
		VALUES ('email_verification', $1, $2, $3, $4)`, id, userID, encrypted, expires)
	return err
}

// LastIssued is when the user's newest link was issued (zero: never).
func (r EmailVerificationRepository) LastIssued(ctx context.Context, userID string) (time.Time, error) {
	var at *time.Time
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT max(created_at) FROM email_verification_tokens WHERE user_id = $1`, userID).Scan(&at)
	if err != nil || at == nil {
		return time.Time{}, err
	}
	return *at, nil
}

// Use consumes a link by its token hash (locked) and marks the account's
// email verified when it still has the address the link was sent to.
// ErrVerificationNotFound for an unknown, used or expired link, or one
// for an address the account no longer has.
func (r EmailVerificationRepository) Use(ctx context.Context, hash string) (userID string, err error) {
	q := connection(ctx, r.Pool)
	var v EmailVerification
	err = q.QueryRow(ctx, `SELECT id, user_id, email, expires_at, used_at FROM email_verification_tokens WHERE token_hash = $1 FOR UPDATE`, hash).
		Scan(&v.ID, &v.UserID, &v.Email, &v.ExpiresAt, &v.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrVerificationNotFound
	}
	if err != nil {
		return "", err
	}
	if v.UsedAt != nil || !time.Now().Before(v.ExpiresAt) {
		return "", ErrVerificationNotFound
	}
	if _, err := q.Exec(ctx, `UPDATE email_verification_tokens SET used_at = now() WHERE id = $1`, v.ID); err != nil {
		return "", err
	}
	tag, err := q.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
		WHERE id = $1 AND lower(btrim(email)) = lower(btrim($2)) AND is_active`, v.UserID, v.Email)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrVerificationNotFound
	}
	return v.UserID, nil
}
