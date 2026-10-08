package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/payment/internal/domain"
)

var ErrHoldNotFound = errors.New("repository: settlement hold not found")

// SettlementHoldRepository stores the hold ledger (00 §6.1). Writes run in
// the use case's transaction after it took the vendor's payout lock.
type SettlementHoldRepository struct{ Pool *pgxpool.Pool }

const holdColumns = `id, vendor_id, vendor_order_id, source_type, source_id, source_version, reason_code, status, payout_claimed,
	acquired_at, release_operation_id, release_source_version, resolution_ref, release_reason, released_at, created_at`

func scanHold(row pgx.Row) (*domain.SettlementHold, error) {
	var h domain.SettlementHold
	err := row.Scan(&h.ID, &h.VendorID, &h.VendorOrderID, &h.SourceType, &h.SourceID, &h.SourceVersion, &h.ReasonCode, &h.Status, &h.PayoutClaimed,
		&h.AcquiredAt, &h.ReleaseOperationID, &h.ReleaseSourceVersion, &h.ResolutionRef, &h.ReleaseReason, &h.ReleasedAt, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHoldNotFound
	}
	return &h, err
}

// Find reads a hold; inside a transaction it is locked.
func (r SettlementHoldRepository) Find(ctx context.Context, id string) (*domain.SettlementHold, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanHold(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+holdColumns+` FROM settlement_holds WHERE id = $1`+lock, id))
}

// FindBySource is the hold a source already holds on a vendor order.
func (r SettlementHoldRepository) FindBySource(ctx context.Context, sourceType, sourceID, vendorOrderID string) (*domain.SettlementHold, error) {
	return scanHold(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+holdColumns+` FROM settlement_holds
		WHERE source_type = $1 AND source_id = $2 AND vendor_order_id = $3`, sourceType, sourceID, vendorOrderID))
}

// PayoutClaimed reports whether a pending or paid payout item already
// claims a credit of the vendor order.
func (r SettlementHoldRepository) PayoutClaimed(ctx context.Context, vendorOrderID string) (bool, error) {
	var claimed bool
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM settlement_entries e
		JOIN payout_item_entries l ON l.entry_id = e.id JOIN payout_items i ON i.id = l.payout_item_id
		WHERE e.vendor_order_id = $1 AND e.amount > 0 AND i.status IN ('pending', 'succeeded'))`, vendorOrderID).Scan(&claimed)
	return claimed, err
}

func (r SettlementHoldRepository) Insert(ctx context.Context, h *domain.SettlementHold) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO settlement_holds (id, vendor_id, vendor_order_id, source_type, source_id, source_version,
		reason_code, status, payout_claimed, acquired_at, release_operation_id, release_source_version, resolution_ref, release_reason, released_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING created_at`,
		h.ID, h.VendorID, h.VendorOrderID, h.SourceType, h.SourceID, h.SourceVersion, h.ReasonCode, h.Status, h.PayoutClaimed, h.AcquiredAt,
		h.ReleaseOperationID, h.ReleaseSourceVersion, h.ResolutionRef, h.ReleaseReason, h.ReleasedAt).Scan(&h.CreatedAt)
}

// Release marks an active hold released.
func (r SettlementHoldRepository) Release(ctx context.Context, h *domain.SettlementHold) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE settlement_holds SET status = 'released', release_operation_id = $2,
		release_source_version = $3, resolution_ref = $4, release_reason = $5, released_at = $6 WHERE id = $1 AND status = 'active'`,
		h.ID, h.ReleaseOperationID, h.ReleaseSourceVersion, h.ResolutionRef, h.ReleaseReason, h.ReleasedAt)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleState
	}
	return err
}

// List lists holds, newest first: active only, or of one vendor order.
func (r SettlementHoldRepository) List(ctx context.Context, vendorOrderID *string, activeOnly bool, limit, offset int) ([]*domain.SettlementHold, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+holdColumns+` FROM settlement_holds
		WHERE ($1::uuid IS NULL OR vendor_order_id = $1) AND (NOT $2 OR status = 'active')
		ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, vendorOrderID, activeOnly, limit, offset)
	return collect(rows, err, scanHold)
}

// ActiveHolds reports which of the vendor orders have an active hold.
func (r *SettlementRepository) ActiveHolds(ctx context.Context, vendorOrderIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(vendorOrderIDs) == 0 {
		return out, nil
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT DISTINCT vendor_order_id::text FROM settlement_holds
		WHERE status = 'active' AND vendor_order_id::text = ANY($1)`, vendorOrderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
