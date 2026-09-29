package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

type AuditLogRepository struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepository(pool *pgxpool.Pool) *AuditLogRepository {
	return &AuditLogRepository{pool: pool}
}

func (r *AuditLogRepository) Create(ctx context.Context, vendorID, actorUserID, action string, reason *string, version int64) error {
	const query = `INSERT INTO vendor_audit_logs (vendor_id, actor_user_id, action, reason, version) VALUES ($1, $2, $3, $4, $5)`
	_, err := connection(ctx, r.pool).Exec(ctx, query, vendorID, actorUserID, action, reason, version)
	return err
}

// List returns a page of recorded decisions, newest first.
func (r *AuditLogRepository) List(ctx context.Context, vendorID string, limit, offset int) ([]*domain.AuditLog, error) {
	const query = `
		SELECT actor_user_id, action, reason, created_at, COALESCE(version,0)
		FROM vendor_audit_logs
		WHERE vendor_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`

	rows, err := connection(ctx, r.pool).Query(ctx, query, vendorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*domain.AuditLog
	for rows.Next() {
		var e domain.AuditLog
		if err := rows.Scan(&e.ActorUserID, &e.Action, &e.Reason, &e.CreatedAt, &e.Version); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
