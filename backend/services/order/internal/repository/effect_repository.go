package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// EffectRepository stores the durable side effects of order transitions.
type EffectRepository struct {
	pool *pgxpool.Pool
}

func NewEffectRepository(pool *pgxpool.Pool) *EffectRepository {
	return &EffectRepository{pool: pool}
}

const effectColumns = `id, order_id, kind, target, payload, status, attempts, next_attempt_at, last_error, created_at`

// effectLease hides a claimed effect from other workers while it runs.
const effectLease = time.Minute

func scanEffects(rows pgx.Rows) ([]*domain.Effect, error) {
	defer rows.Close()
	out := []*domain.Effect{}
	for rows.Next() {
		var e domain.Effect
		var payload []byte
		if err := rows.Scan(&e.ID, &e.OrderID, &e.Kind, &e.Target, &payload, &e.Status, &e.Attempts, &e.NextAttemptAt, &e.LastError, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// Enqueue adds effects in the caller's transaction. An effect that already
// exists for (order, kind, target) is left untouched.
func (r *EffectRepository) Enqueue(ctx context.Context, effects ...domain.Effect) error {
	q := connection(ctx, r.pool)
	for _, e := range effects {
		payload := []byte(e.Payload)
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if _, err := q.Exec(ctx, `INSERT INTO order_effects (order_id, kind, target, payload) VALUES ($1, $2, $3, $4)
			ON CONFLICT (order_id, kind, target) DO NOTHING`, e.OrderID, e.Kind, e.Target, payload); err != nil {
			return err
		}
	}
	return nil
}

// ClaimDue leases up to limit due effects, optionally only one order's.
func (r *EffectRepository) ClaimDue(ctx context.Context, orderID string, limit int) ([]*domain.Effect, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `
		UPDATE order_effects SET next_attempt_at = now() + $3::double precision * interval '1 second', updated_at = now()
		WHERE id IN (
			SELECT id FROM order_effects
			WHERE status = 'pending' AND next_attempt_at <= now() AND ($1 = '' OR order_id::text = $1)
			ORDER BY next_attempt_at, id LIMIT $2
			FOR UPDATE SKIP LOCKED)
		RETURNING `+effectColumns, orderID, limit, effectLease.Seconds())
	if err != nil {
		return nil, err
	}
	return scanEffects(rows)
}

func (r *EffectRepository) MarkDone(ctx context.Context, id string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE order_effects SET status = 'done', done_at = now(), last_error = NULL, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id)
	return err
}

// RecordFailure counts a failed attempt and schedules the next, or parks
// the effect when park is set or retries are exhausted.
func (r *EffectRepository) RecordFailure(ctx context.Context, id, reason string, park bool) (parked bool, err error) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	var attempts int
	err = connection(ctx, r.pool).QueryRow(ctx, `UPDATE order_effects SET attempts = attempts + 1, last_error = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending' RETURNING attempts`, id, reason).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if park || attempts >= domain.MaxEffectAttempts {
		_, err = connection(ctx, r.pool).Exec(ctx, `UPDATE order_effects SET status = 'parked', updated_at = now() WHERE id = $1`, id)
		return true, err
	}
	_, err = connection(ctx, r.pool).Exec(ctx, `UPDATE order_effects SET next_attempt_at = now() + $2::double precision * interval '1 second' WHERE id = $1`,
		id, domain.EffectBackoff(attempts).Seconds())
	return false, err
}

// Replay puts a parked effect back in the queue (admin action).
func (r *EffectRepository) Replay(ctx context.Context, id string) (bool, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE order_effects SET status = 'pending', attempts = 0, next_attempt_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'parked'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *EffectRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.Effect, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+effectColumns+` FROM order_effects WHERE order_id = $1 ORDER BY created_at, id`, orderID)
	if err != nil {
		return nil, err
	}
	return scanEffects(rows)
}

// ListParked returns effects that need an operator.
func (r *EffectRepository) ListParked(ctx context.Context, limit, offset int) ([]*domain.Effect, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+effectColumns+` FROM order_effects WHERE status = 'parked'
		ORDER BY updated_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	return scanEffects(rows)
}

func (r *EffectRepository) Stats(ctx context.Context) (domain.EffectStats, error) {
	var s domain.EffectStats
	err := r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'pending'), count(*) FILTER (WHERE status = 'parked'),
		min(created_at) FILTER (WHERE status = 'pending') FROM order_effects WHERE status IN ('pending', 'parked')`).
		Scan(&s.Pending, &s.Parked, &s.OldestPending)
	return s, err
}
