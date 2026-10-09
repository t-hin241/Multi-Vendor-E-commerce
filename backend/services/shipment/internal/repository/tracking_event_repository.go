package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/shipment/internal/domain"
)

const trackingEventColumns = `id, shipment_id, status, note, actor_id, actor_role, event_key, occurred_at, created_at`

type TrackingEventRepository struct {
	pool *pgxpool.Pool
}

func NewTrackingEventRepository(pool *pgxpool.Pool) *TrackingEventRepository {
	return &TrackingEventRepository{pool: pool}
}

// Insert appends a timeline entry. With an event key it is written once:
// a second insert with the same key reports inserted=false.
func (r *TrackingEventRepository) Insert(ctx context.Context, e *domain.TrackingEvent) (bool, error) {
	if e.ActorRole == "" {
		e.ActorRole = domain.ActorSystem
	}
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO shipment_tracking_events (shipment_id, status, note, actor_id, actor_role, event_key, occurred_at, membership_version)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, now()), $8)
		ON CONFLICT (shipment_id, event_key) WHERE event_key IS NOT NULL DO NOTHING
		RETURNING id, occurred_at, created_at`,
		e.ShipmentID, e.Status, e.Note, e.ActorID, e.ActorRole, e.EventKey, nullTime(e.OccurredAt), shopaccess.UsedMembershipVersion(ctx)).Scan(&e.ID, &e.OccurredAt, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Exists reports whether a keyed event was already recorded.
func (r *TrackingEventRepository) Exists(ctx context.Context, shipmentID, key string) (bool, error) {
	var ok bool
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM shipment_tracking_events WHERE shipment_id = $1 AND event_key = $2)`, shipmentID, key).Scan(&ok)
	return ok, err
}

func (r *TrackingEventRepository) ListForShipment(ctx context.Context, shipmentID string) ([]*domain.TrackingEvent, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+trackingEventColumns+` FROM shipment_tracking_events WHERE shipment_id = $1 ORDER BY occurred_at, created_at`, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.TrackingEvent{}
	for rows.Next() {
		var e domain.TrackingEvent
		if err := rows.Scan(&e.ID, &e.ShipmentID, &e.Status, &e.Note, &e.ActorID, &e.ActorRole, &e.EventKey, &e.OccurredAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
