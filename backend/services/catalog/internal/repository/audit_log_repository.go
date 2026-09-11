package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
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
