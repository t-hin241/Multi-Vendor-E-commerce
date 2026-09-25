package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type AuditLogRepository struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepository(pool *pgxpool.Pool) *AuditLogRepository {
	return &AuditLogRepository{pool: pool}
}

func (r *AuditLogRepository) Create(ctx context.Context, productID, actorUserID, action string, reason *string) error {
	const query = `INSERT INTO product_audit_logs (product_id, actor_user_id, action, reason) VALUES ($1, $2, $3, $4)`
	_, err := r.pool.Exec(ctx, query, productID, actorUserID, action, reason)
	return err
}

// List returns every recorded decision for one product, newest first — the
// full moderation history admin currently has no way to see beyond the
// single rejection_reason on the product row itself.
func (r *AuditLogRepository) List(ctx context.Context, productID string) ([]*domain.AuditLog, error) {
	const query = `
		SELECT actor_user_id, action, reason, created_at
		FROM product_audit_logs
		WHERE product_id = $1
		ORDER BY created_at DESC`

	rows, err := r.pool.Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*domain.AuditLog
	for rows.Next() {
		var e domain.AuditLog
		if err := rows.Scan(&e.ActorUserID, &e.Action, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
