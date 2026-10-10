package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTOTPNotFound: the admin has no (or no confirmed) authenticator.
var ErrTOTPNotFound = errors.New("repository: totp not found")

// ErrStaleState: the authenticator changed meanwhile (already confirmed).
var ErrStaleState = errors.New("repository: totp changed")

// TOTPRepository stores PW-028 authenticators and recovery codes.
type TOTPRepository struct{ Pool *pgxpool.Pool }

// TOTP is one admin's authenticator.
type TOTP struct {
	UserID       string
	Ciphertext   []byte
	ConfirmedAt  *time.Time
	LastUsedStep int64
}

// Find reads (and, in a transaction, locks) the admin's authenticator.
func (r TOTPRepository) Find(ctx context.Context, userID string) (*TOTP, error) {
	q := `SELECT user_id, secret_ciphertext, confirmed_at, last_used_step FROM admin_totp WHERE user_id = $1` + lockUser(ctx)
	var t TOTP
	err := connection(ctx, r.Pool).QueryRow(ctx, q, userID).Scan(&t.UserID, &t.Ciphertext, &t.ConfirmedAt, &t.LastUsedStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTOTPNotFound
	}
	return &t, err
}

// Start stores a new, unconfirmed secret (replacing an unconfirmed one).
func (r TOTPRepository) Start(ctx context.Context, userID string, ciphertext []byte) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO admin_totp (user_id, secret_ciphertext) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET secret_ciphertext = EXCLUDED.secret_ciphertext, last_used_step = 0, updated_at = now()
		WHERE admin_totp.confirmed_at IS NULL`, userID, ciphertext)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// Confirm marks the authenticator confirmed at step and stores the hashed
// recovery codes, replacing older ones.
func (r TOTPRepository) Confirm(ctx context.Context, userID string, step int64, codeHashes []string) error {
	q := connection(ctx, r.Pool)
	tag, err := q.Exec(ctx, `UPDATE admin_totp SET confirmed_at = now(), last_used_step = $2, updated_at = now()
		WHERE user_id = $1 AND confirmed_at IS NULL`, userID, step)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	if _, err := q.Exec(ctx, `DELETE FROM admin_totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := q.Exec(ctx, `INSERT INTO admin_totp_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return err
		}
	}
	return nil
}

// UseStep records the step a code was accepted for.
func (r TOTPRepository) UseStep(ctx context.Context, userID string, step int64) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE admin_totp SET last_used_step = $2, updated_at = now() WHERE user_id = $1 AND last_used_step < $2`,
		userID, step)
	return err
}

// UseRecoveryCode spends one unused recovery code; false when none matched.
func (r TOTPRepository) UseRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE admin_totp_recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, codeHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RecoveryCodesLeft counts the unused recovery codes.
func (r TOTPRepository) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	var n int
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT count(*) FROM admin_totp_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}
