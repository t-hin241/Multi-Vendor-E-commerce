package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

var ErrRefreshTokenNotFound = errors.New("repository: refresh token not found")

func (r *RefreshTokenRepository) Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	const query = `INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, query, userID, tokenHash, expiresAt)
	return err
}

func (r *RefreshTokenRepository) FindActiveByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	const query = `
		SELECT id, user_id, token_hash, expires_at, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`

	var rt RefreshToken
	err := r.pool.QueryRow(ctx, query, tokenHash).Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, err
	}
	return &rt, nil
}

func (r *RefreshTokenRepository) Revoke(ctx context.Context, id string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}

// RevokeAllForUser revokes every active refresh token for a user, e.g. after
// a password reset, so previously issued sessions can't keep refreshing.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := r.pool.Exec(ctx, query, userID)
	return err
}

func (r *RefreshTokenRepository) RevokeByHash(ctx context.Context, tokenHash string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`
	_, err := r.pool.Exec(ctx, query, tokenHash)
	return err
}
