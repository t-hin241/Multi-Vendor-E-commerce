package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

// EvidenceRepository stores PW-038 failure report evidence.
type EvidenceRepository struct{ Pool *pgxpool.Pool }

const evidenceColumns = `id, owner_id, owner_role, shipment_id, report_kind, object_key, content_type, size_bytes, state, created_at`

func (r EvidenceRepository) list(ctx context.Context, where string, args ...any) ([]*domain.Evidence, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+evidenceColumns+` FROM shipment_evidence `+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*domain.Evidence, error) {
		var e domain.Evidence
		err := row.Scan(&e.ID, &e.OwnerID, &e.OwnerRole, &e.ShipmentID, &e.ReportKind, &e.ObjectKey, &e.ContentType, &e.SizeBytes, &e.State, &e.CreatedAt)
		return &e, err
	})
}

// Create records an object already written to the bucket.
func (r EvidenceRepository) Create(ctx context.Context, e *domain.Evidence) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO shipment_evidence (owner_id, owner_role, shipment_id, object_key, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, state, created_at`, e.OwnerID, e.OwnerRole, e.ShipmentID, e.ObjectKey, e.ContentType, e.SizeBytes).
		Scan(&e.ID, &e.State, &e.CreatedAt)
}

// Attach links the owner's unattached uploads for the shipment to its
// report and returns how many were linked.
func (r EvidenceRepository) Attach(ctx context.Context, ids []string, ownerID, shipmentID, kind string) (int64, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE shipment_evidence SET state = 'attached', report_kind = $4, attached_at = now()
		WHERE id::text = ANY($1) AND owner_id = $2 AND shipment_id = $3 AND state = 'uploaded'`, ids, ownerID, shipmentID, kind)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ForShipment lists the attached evidence of a shipment, and the uploads
// of owner not attached yet.
func (r EvidenceRepository) ForShipment(ctx context.Context, shipmentID, owner string) ([]*domain.Evidence, error) {
	return r.list(ctx, `WHERE shipment_id = $1 AND (state = 'attached' OR (state = 'uploaded' AND owner_id::text = $2)) ORDER BY created_at, id`, shipmentID, owner)
}

// Orphans lists uploads never attached before t.
func (r EvidenceRepository) Orphans(ctx context.Context, before time.Time, limit int) ([]*domain.Evidence, error) {
	return r.list(ctx, `WHERE state = 'uploaded' AND created_at < $1 ORDER BY created_at, id LIMIT $2`, before, limit)
}

// MarkDeleted tombstones a row still in state from; false when it changed.
func (r EvidenceRepository) MarkDeleted(ctx context.Context, id, from string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE shipment_evidence SET state = 'deleted', deleted_at = now() WHERE id = $1 AND state = $2`, id, from)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
