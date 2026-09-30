package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RetentionRepository deletes idle carts and old checkout operations in
// bounded batches.
type RetentionRepository struct {
	pool *pgxpool.Pool
}

func NewRetentionRepository(pool *pgxpool.Pool) *RetentionRepository {
	return &RetentionRepository{pool: pool}
}

// PurgeIdleCarts deletes carts (and, by cascade, their lines) untouched
// since idleBefore. A cart with a checkout operation that has not been
// consumed yet is kept: Order may still be retrying consume for an order it
// created from that cart. Rows locked by an in-flight mutation are skipped.
func (r *RetentionRepository) PurgeIdleCarts(ctx context.Context, idleBefore time.Time, limit int) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM carts WHERE id IN (
			SELECT c.id FROM carts c
			WHERE c.updated_at < $1
			  AND NOT EXISTS (
				SELECT 1 FROM cart_checkout_operations o
				WHERE o.cart_id = c.id AND o.consumed_at IS NULL)
			ORDER BY c.updated_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED)`, idleBefore, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeOperations deletes checkout operations created before createdBefore.
// The retention window must stay longer than Order's consume retry horizon.
func (r *RetentionRepository) PurgeOperations(ctx context.Context, createdBefore time.Time, limit int) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM cart_checkout_operations WHERE operation_id IN (
			SELECT operation_id FROM cart_checkout_operations
			WHERE created_at < $1
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED)`, createdBefore, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
