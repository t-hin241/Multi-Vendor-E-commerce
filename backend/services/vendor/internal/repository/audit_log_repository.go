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

func (r *AuditLogRepository) Create(ctx context.Context, vendorID, actorUserID, action string, reason *string) error {
	const query = `INSERT INTO vendor_audit_logs (vendor_id, actor_user_id, action, reason) VALUES ($1, $2, $3, $4)`
	_, err := r.pool.Exec(ctx, query, vendorID, actorUserID, action, reason)
	return err
}
