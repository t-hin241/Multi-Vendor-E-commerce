package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/cart/internal/domain"
)

type CartRepository struct {
	pool *pgxpool.Pool
}

func NewCartRepository(pool *pgxpool.Pool) *CartRepository {
	return &CartRepository{pool: pool}
}

const cartColumns = `id, user_id, version, created_at, updated_at`

func scanCart(row pgx.Row) (*domain.Cart, error) {
	var c domain.Cart
	if err := row.Scan(&c.ID, &c.UserID, &c.Version, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// GetOrCreateForUser returns the buyer's cart, creating an empty one the
// first time. ON CONFLICT DO NOTHING plus a re-select keeps this safe under
// a concurrent first add.
func (r *CartRepository) GetOrCreateForUser(ctx context.Context, userID string) (*domain.Cart, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	db := connection(ctx, r.pool)

	c, err := scanCart(db.QueryRow(ctx, `
		INSERT INTO carts (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING
		RETURNING `+cartColumns, userID))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return scanCart(db.QueryRow(ctx, `SELECT `+cartColumns+` FROM carts WHERE user_id = $1`, userID))
}

// LockForUser returns the buyer's cart row locked FOR UPDATE, creating it if
// needed. Every cart mutation and checkout operation takes this lock first,
// so line-count/quantity limits, version checks and consume never race each
// other. Must run inside Transactions.Run.
func (r *CartRepository) LockForUser(ctx context.Context, userID string) (*domain.Cart, error) {
	if !inTransaction(ctx) {
		return nil, errors.New("cart: LockForUser requires a transaction")
	}
	if _, err := r.GetOrCreateForUser(ctx, userID); err != nil {
		return nil, err
	}
	return scanCart(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+cartColumns+` FROM carts WHERE user_id = $1 FOR UPDATE`, userID))
}

// BumpVersion records that the cart changed and returns the new version.
func (r *CartRepository) BumpVersion(ctx context.Context, cartID string) (int64, error) {
	ctx, cancel := statementContext(ctx)
	defer cancel()
	var version int64
	err := connection(ctx, r.pool).QueryRow(ctx,
		`UPDATE carts SET version = version + 1, updated_at = now() WHERE id = $1 RETURNING version`, cartID).Scan(&version)
	return version, err
}
