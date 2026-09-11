package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PasswordResetToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type PasswordResetRepository struct {
	pool *pgxpool.Pool
}

func NewPasswordResetRepository(pool *pgxpool.Pool) *PasswordResetRepository {
	return &PasswordResetRepository{pool: pool}
}

var ErrPasswordResetTokenNotFound = errors.New("repository: password reset token not found")

func (r *PasswordResetRepository) Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	const query = `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, query, userID, tokenHash, expiresAt)
	return err
}

func (r *PasswordResetRepository) FindUsableByHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error) {
	const query = `
		SELECT id, user_id, token_hash, expires_at, used_at
		FROM password_reset_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`

	var t PasswordResetToken
	err := r.pool.QueryRow(ctx, query, tokenHash).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.UsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPasswordResetTokenNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *PasswordResetRepository) MarkUsed(ctx context.Context, id string) error {
	const query = `UPDATE password_reset_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}
