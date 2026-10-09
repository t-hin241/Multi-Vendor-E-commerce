package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// SourceHoldRepository stores the settlement holds of returns and refunds
// (PW-001).
type SourceHoldRepository struct{ Pool *pgxpool.Pool }

const sourceHoldColumns = `source_type, source_id::text, order_id::text, vendor_id::text, vendor_order_id::text, hold_id::text, status, note, updated_at`

func scanSourceHold(row pgx.Row) (*domain.SourceHold, error) {
	var h domain.SourceHold
	err := row.Scan(&h.SourceType, &h.SourceID, &h.OrderID, &h.VendorID, &h.VendorOrderID, &h.HoldID, &h.Status, &h.Note, &h.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &h, err
}

// Get returns the source's hold, or nil.
func (r SourceHoldRepository) Get(ctx context.Context, sourceType, sourceID string) (*domain.SourceHold, error) {
	return scanSourceHold(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+sourceHoldColumns+` FROM source_settlement_holds
		WHERE source_type = $1 AND source_id = $2`, sourceType, sourceID))
}

// Prepare records a preparing hold once per source; false when it exists.
func (r SourceHoldRepository) Prepare(ctx context.Context, h *domain.SourceHold) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO source_settlement_holds
		(source_type, source_id, order_id, vendor_id, vendor_order_id, hold_id, status) VALUES ($1, $2, $3, $4, $5, $6, 'preparing')
		ON CONFLICT (source_type, source_id) DO NOTHING`, h.SourceType, h.SourceID, h.OrderID, h.VendorID, h.VendorOrderID, h.HoldID)
	return tag.RowsAffected() == 1, err
}

// SetStatus moves the hold to `to` if it is in one of `from`.
func (r SourceHoldRepository) SetStatus(ctx context.Context, sourceType, sourceID string, from []string, to string, note *string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE source_settlement_holds SET status = $4, note = COALESCE($5, note), updated_at = now()
		WHERE source_type = $1 AND source_id = $2 AND status = ANY($3)`, sourceType, sourceID, from, to, note)
	return tag.RowsAffected() == 1, err
}

// SourceRef names a source and its order (for the order lock).
type SourceRef struct {
	SourceType string
	SourceID   string
	OrderID    string
}

// ListMissing lists open returns and refunds of a vendor order that have
// no hold yet (opened before the ledger was on): the backfill.
func (r SourceHoldRepository) ListMissing(ctx context.Context, limit int) ([]SourceRef, error) {
	return listRefs(ctx, r.Pool, `
		SELECT 'return', rr.id::text, rr.order_id::text FROM return_requests rr
		WHERE rr.status NOT IN ('rejected', 'refunded')
		  AND NOT EXISTS (SELECT 1 FROM source_settlement_holds h WHERE h.source_type = 'return' AND h.source_id = rr.id)
		UNION ALL
		SELECT 'refund', f.id::text, f.order_id::text FROM order_refunds f
		WHERE f.status IN ('requested', 'submitted') AND f.vendor_order_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM source_settlement_holds h WHERE h.source_type = 'refund' AND h.source_id = f.id)
		LIMIT $1`, limit)
}

// ListUnreleased lists open holds whose return or refund is final.
func (r SourceHoldRepository) ListUnreleased(ctx context.Context, limit int) ([]SourceRef, error) {
	return listRefs(ctx, r.Pool, `
		SELECT h.source_type, h.source_id::text, h.order_id::text FROM source_settlement_holds h
		WHERE h.status IN ('preparing', 'active', 'needs_review') AND (
			(h.source_type = 'return' AND EXISTS (SELECT 1 FROM return_requests rr WHERE rr.id = h.source_id AND rr.status IN ('rejected', 'refunded')))
			OR (h.source_type = 'refund' AND EXISTS (SELECT 1 FROM order_refunds f WHERE f.id = h.source_id AND f.status IN ('succeeded', 'failed', 'rejected'))))
		LIMIT $1`, limit)
}

func listRefs(ctx context.Context, pool *pgxpool.Pool, q string, limit int) ([]SourceRef, error) {
	rows, err := connection(ctx, pool).Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceRef{}
	for rows.Next() {
		var ref SourceRef
		if err := rows.Scan(&ref.SourceType, &ref.SourceID, &ref.OrderID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// SourceHoldCounts is the backlog report: holds waiting on Payment or an
// operator, and open sources still without a hold (the backfill is done
// when missing is zero; only then may Payment stop asking Order).
type SourceHoldCounts struct {
	Preparing, NeedsReview, Releasing, Missing int64
}

func (r SourceHoldRepository) Counts(ctx context.Context) (SourceHoldCounts, error) {
	var c SourceHoldCounts
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT
		(SELECT count(*) FROM source_settlement_holds WHERE status = 'preparing'),
		(SELECT count(*) FROM source_settlement_holds WHERE status = 'needs_review'),
		(SELECT count(*) FROM source_settlement_holds WHERE status = 'releasing'),
		(SELECT count(*) FROM return_requests rr WHERE rr.status NOT IN ('rejected', 'refunded')
			AND NOT EXISTS (SELECT 1 FROM source_settlement_holds h WHERE h.source_type = 'return' AND h.source_id = rr.id))
		+ (SELECT count(*) FROM order_refunds f WHERE f.status IN ('requested', 'submitted') AND f.vendor_order_id IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM source_settlement_holds h WHERE h.source_type = 'refund' AND h.source_id = f.id))`).
		Scan(&c.Preparing, &c.NeedsReview, &c.Releasing, &c.Missing)
	return c, err
}
