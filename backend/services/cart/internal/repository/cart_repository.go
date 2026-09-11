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

// GetOrCreateForUser returns the buyer's cart, creating an empty one the
// first time they add an item. ON CONFLICT DO NOTHING plus a re-select
// keeps this safe under a concurrent double-add without needing an
// explicit transaction.
func (r *CartRepository) GetOrCreateForUser(ctx context.Context, userID string) (*domain.Cart, error) {
	const insertQuery = `
		INSERT INTO carts (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING
		RETURNING id, user_id, created_at, updated_at`

	var c domain.Cart
	err := r.pool.QueryRow(ctx, insertQuery, userID).Scan(&c.ID, &c.UserID, &c.CreatedAt, &c.UpdatedAt)
	if err == nil {
		return &c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	const selectQuery = `SELECT id, user_id, created_at, updated_at FROM carts WHERE user_id = $1`
	err = r.pool.QueryRow(ctx, selectQuery, userID).Scan(&c.ID, &c.UserID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
