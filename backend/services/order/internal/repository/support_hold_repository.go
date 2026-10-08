package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// SupportHoldRepository tracks support cases' settlement holds (PW-001).
// Hold columns change without touching the case version, so an effect
// confirming a hold never makes an admin's expected_version stale.
type SupportHoldRepository struct{ Pool *pgxpool.Pool }

const caseHoldColumns = `id, order_id, hold_id, hold_status, hold_note, hold_updated_at`

func scanCaseHold(row pgx.Row) (*domain.CaseHold, error) {
	var h domain.CaseHold
	var holdID, status *string
	var updated *time.Time
	if err := row.Scan(&h.CaseID, &h.OrderID, &holdID, &status, &h.Note, &updated); err != nil {
		return nil, err
	}
	if holdID == nil {
		return nil, nil
	}
	h.HoldID, h.Status = *holdID, *status
	if updated != nil {
		h.UpdatedAt = *updated
	}
	return &h, nil
}

// Get is the case's hold, or nil when it has none.
func (r SupportHoldRepository) Get(ctx context.Context, caseID string) (*domain.CaseHold, error) {
	h, err := scanCaseHold(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+caseHoldColumns+` FROM support_cases WHERE id = $1`, caseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSupportCaseNotFound
	}
	return h, err
}

// Prepare gives a case without a hold its hold id, in state preparing.
func (r SupportHoldRepository) Prepare(ctx context.Context, caseID, holdID string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE support_cases SET hold_id = $2, hold_status = 'preparing', hold_note = NULL,
		hold_updated_at = now() WHERE id = $1 AND hold_id IS NULL`, caseID, holdID)
	return err == nil && tag.RowsAffected() == 1, err
}

// SetStatus moves a hold from one of from to to; false when it was not in
// any of them.
func (r SupportHoldRepository) SetStatus(ctx context.Context, caseID string, from []string, to string, note *string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE support_cases SET hold_status = $3, hold_note = $4, hold_updated_at = now()
		WHERE id = $1 AND hold_status = ANY($2)`, caseID, from, to, note)
	return err == nil && tag.RowsAffected() == 1, err
}

// CaseRef names a case and the order whose lock guards it.
type CaseRef struct{ CaseID, OrderID string }

func (r SupportHoldRepository) refs(ctx context.Context, sql string, limit int) ([]CaseRef, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, sql, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaseRef{}
	for rows.Next() {
		var ref CaseRef
		if err := rows.Scan(&ref.CaseID, &ref.OrderID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// ListMissing: open cases that may affect money but have no hold yet
// (opened before the ledger was on).
func (r SupportHoldRepository) ListMissing(ctx context.Context, limit int) ([]CaseRef, error) {
	return r.refs(ctx, `SELECT id, order_id FROM support_cases WHERE financial_hold AND hold_id IS NULL AND status <> 'closed'
		ORDER BY created_at LIMIT $1`, limit)
}

// ListUnreleased: closed cases whose hold was never sent for release.
func (r SupportHoldRepository) ListUnreleased(ctx context.Context, limit int) ([]CaseRef, error) {
	return r.refs(ctx, `SELECT id, order_id FROM support_cases WHERE status = 'closed' AND hold_status IN ('preparing', 'active', 'needs_review')
		ORDER BY closed_at LIMIT $1`, limit)
}

// HoldCounts feed the worker report: holds waiting for Payment and holds
// an operator must review.
func (r SupportHoldRepository) Counts(ctx context.Context) (preparing, needsReview, releasing int64, err error) {
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE hold_status = 'preparing'), count(*) FILTER (WHERE hold_status = 'needs_review'),
		count(*) FILTER (WHERE hold_status = 'releasing') FROM support_cases WHERE hold_status IN ('preparing', 'needs_review', 'releasing')`).
		Scan(&preparing, &needsReview, &releasing)
	return preparing, needsReview, releasing, err
}
