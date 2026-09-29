package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RefreshToken struct {
	FamilyID  string
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

func (r *RefreshTokenRepository) OwnerByHash(ctx context.Context, hash string) (string, error) {
	var id string
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT user_id FROM refresh_tokens WHERE token_hash=$1`, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRefreshTokenNotFound
	}
	return id, err
}
func (r *RefreshTokenRepository) RevokeSession(ctx context.Context, userID, sessionID string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND family_id=$2 AND revoked_at IS NULL`, userID, sessionID)
	return err
}

var ErrRefreshTokenNotFound = errors.New("repository: refresh token not found")

func (r *RefreshTokenRepository) Create(ctx context.Context, userID, tokenHash string, expiresAt time.Time, familyID string) error {
	const query = `INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id) VALUES ($1, $2, $3, $4)`
	_, err := connection(ctx, r.pool).Exec(ctx, query, userID, tokenHash, expiresAt, familyID)
	return err
}

func (r *RefreshTokenRepository) FindActiveByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	const query = `
		SELECT id, user_id, token_hash, expires_at, revoked_at, COALESCE(family_id, '')
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`

	var rt RefreshToken
	err := connection(ctx, r.pool).QueryRow(ctx, query, tokenHash).Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.RevokedAt, &rt.FamilyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, err
	}
	return &rt, nil
}

func (r *RefreshTokenRepository) Revoke(ctx context.Context, id string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL AND expires_at > now()`
	tag, err := connection(ctx, r.pool).Exec(ctx, query, id)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrRefreshTokenNotFound
	}
	return err
}

// RevokeAllForUser revokes every unrevoked refresh token belonging to the user.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := connection(ctx, r.pool).Exec(ctx, query, userID)
	return err
}

func (r *RefreshTokenRepository) RevokeByHash(ctx context.Context, tokenHash string) error {
	const query = `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1) AND revoked_at IS NULL`
	_, err := connection(ctx, r.pool).Exec(ctx, query, tokenHash)
	return err
}

func (r *RefreshTokenRepository) SessionActive(ctx context.Context, userID, role, familyID string) (bool, error) {
	var active bool
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM refresh_tokens t JOIN users u ON u.id=t.user_id WHERE t.user_id=$1 AND u.role=$2 AND u.is_active AND t.family_id=$3 AND t.revoked_at IS NULL AND t.expires_at>now())`, userID, role, familyID).Scan(&active)
	return active, err
}
