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

func (r *AuditLogRepository) Create(ctx context.Context, vendorID, actorUserID, action string, reason *string) error {
	const query = `INSERT INTO vendor_audit_logs (vendor_id, actor_user_id, action, reason) VALUES ($1, $2, $3, $4)`
	_, err := r.pool.Exec(ctx, query, vendorID, actorUserID, action, reason)
	return err
}

// List returns every recorded decision for one vendor, newest first — the
// full moderation history admin currently has no way to see beyond the
// single rejection_reason on the vendor row itself.
func (r *AuditLogRepository) List(ctx context.Context, vendorID string) ([]*domain.AuditLog, error) {
	const query = `
		SELECT actor_user_id, action, reason, created_at
		FROM vendor_audit_logs
		WHERE vendor_id = $1
		ORDER BY created_at DESC`

	rows, err := r.pool.Query(ctx, query, vendorID)
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
