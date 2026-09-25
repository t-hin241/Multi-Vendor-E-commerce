package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/notification/internal/domain"
)

type NotificationRepository struct {
	pool *pgxpool.Pool
}

func NewNotificationRepository(pool *pgxpool.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	const query = `
		INSERT INTO notifications (user_id, type, reference_id, status, fail_reason)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`

	return r.pool.QueryRow(ctx, query, n.UserID, n.Type, n.ReferenceID, n.Status, n.FailReason).Scan(&n.ID, &n.CreatedAt)
}

func (r *NotificationRepository) List(ctx context.Context, limit, offset int) ([]*domain.Notification, error) {
	const query = `
		SELECT id, user_id, type, reference_id, status, fail_reason, created_at
		FROM notifications ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := r.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Notification
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.ReferenceID, &n.Status, &n.FailReason, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}
