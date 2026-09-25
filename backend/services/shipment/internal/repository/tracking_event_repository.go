package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

const trackingEventColumns = `id, shipment_id, status, note, created_at`

type TrackingEventRepository struct {
	pool *pgxpool.Pool
}

func NewTrackingEventRepository(pool *pgxpool.Pool) *TrackingEventRepository {
	return &TrackingEventRepository{pool: pool}
}

func (r *TrackingEventRepository) Insert(ctx context.Context, e *domain.TrackingEvent) error {
	const query = `INSERT INTO shipment_tracking_events (shipment_id, status, note) VALUES ($1, $2, $3) RETURNING id, created_at`
	return r.pool.QueryRow(ctx, query, e.ShipmentID, e.Status, e.Note).Scan(&e.ID, &e.CreatedAt)
}

func (r *TrackingEventRepository) ListForShipment(ctx context.Context, shipmentID string) ([]*domain.TrackingEvent, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+trackingEventColumns+` FROM shipment_tracking_events WHERE shipment_id = $1 ORDER BY created_at ASC`, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.TrackingEvent
	for rows.Next() {
		var e domain.TrackingEvent
		if err := rows.Scan(&e.ID, &e.ShipmentID, &e.Status, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
