package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// CartConsumptionRepository stores Order's durable cart-consume tasks.
type CartConsumptionRepository struct {
	pool *pgxpool.Pool
}

func NewCartConsumptionRepository(pool *pgxpool.Pool) *CartConsumptionRepository {
	return &CartConsumptionRepository{pool: pool}
}

// claimLease hides a claimed task from other workers while it is processed.
const claimLease = time.Minute

func insertCartConsumption(ctx context.Context, tx pgx.Tx, orderID string, c *domain.CartConsumption) error {
	lines, err := json.Marshal(c.Lines)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO cart_consumptions (order_id, buyer_id, operation_id, lines, status)
		VALUES ($1, $2, $3, $4, 'held')`, orderID, c.BuyerID, c.OperationID, lines)
	if err != nil {
		return fmt.Errorf("insert cart consumption: %w", err)
	}
	c.OrderID, c.Status = orderID, domain.CartConsumptionHeld
	return nil
}

const consumptionColumns = `order_id, buyer_id, operation_id, lines, status, attempts, next_attempt_at, last_error, created_at`

func scanConsumptions(rows pgx.Rows) ([]*domain.CartConsumption, error) {
	defer rows.Close()
	var out []*domain.CartConsumption
	for rows.Next() {
		var (
			c     domain.CartConsumption
			lines []byte
		)
		if err := rows.Scan(&c.OrderID, &c.BuyerID, &c.OperationID, &lines, &c.Status, &c.Attempts, &c.NextAttemptAt, &c.LastError, &c.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(lines, &c.Lines); err != nil {
			return nil, fmt.Errorf("decode cart consumption lines of order %s: %w", c.OrderID, err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *CartConsumptionRepository) query(ctx context.Context, sql string, args ...any) ([]*domain.CartConsumption, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := connection(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return scanConsumptions(rows)
}

func (r *CartConsumptionRepository) exec(ctx context.Context, sql string, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := connection(ctx, r.pool).Exec(ctx, sql, args...)
	return err
}

// ListOpenByBuyer returns the buyer's held and pending tasks.
func (r *CartConsumptionRepository) ListOpenByBuyer(ctx context.Context, buyerID string) ([]*domain.CartConsumption, error) {
	return r.query(ctx, `SELECT `+consumptionColumns+` FROM cart_consumptions
		WHERE buyer_id = $1 AND status IN ('held', 'pending') ORDER BY created_at`, buyerID)
}

// Activate makes a held task due now: the order stands.
func (r *CartConsumptionRepository) Activate(ctx context.Context, orderID string) error {
	return r.exec(ctx, `UPDATE cart_consumptions SET status = 'pending', next_attempt_at = now(), updated_at = now()
		WHERE order_id = $1 AND status = 'held'`, orderID)
}

// Cancel drops a held task: the checkout failed and the cart stays as is.
func (r *CartConsumptionRepository) Cancel(ctx context.Context, orderID string) error {
	return r.exec(ctx, `UPDATE cart_consumptions SET status = 'cancelled', updated_at = now()
		WHERE order_id = $1 AND status = 'held'`, orderID)
}

func (r *CartConsumptionRepository) MarkConsumed(ctx context.Context, orderID string) error {
	return r.exec(ctx, `UPDATE cart_consumptions SET status = 'consumed', consumed_at = now(), last_error = NULL, updated_at = now()
		WHERE order_id = $1 AND status IN ('pending', 'parked')`, orderID)
}

// RecordFailure counts a failed attempt and schedules the next one, or
// parks the task when park is set or retries are exhausted.
func (r *CartConsumptionRepository) RecordFailure(ctx context.Context, orderID, reason string, park bool) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var attempts int
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE cart_consumptions SET attempts = attempts + 1, last_error = $2, updated_at = now()
		WHERE order_id = $1 AND status = 'pending' RETURNING attempts`, orderID, reason).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if park || attempts >= domain.MaxCartConsumeAttempts {
		return r.exec(ctx, `UPDATE cart_consumptions SET status = 'parked', updated_at = now() WHERE order_id = $1 AND status = 'pending'`, orderID)
	}
	return r.exec(ctx, `UPDATE cart_consumptions SET next_attempt_at = now() + $2::double precision * interval '1 second' WHERE order_id = $1`,
		orderID, domain.CartConsumeBackoff(attempts).Seconds())
}

// ClaimDue leases up to limit due pending tasks. A crashed worker's lease
// simply expires and the task becomes due again.
func (r *CartConsumptionRepository) ClaimDue(ctx context.Context, limit int) ([]*domain.CartConsumption, error) {
	return r.query(ctx, `
		UPDATE cart_consumptions SET next_attempt_at = now() + $2::double precision * interval '1 second'
		WHERE order_id IN (
			SELECT order_id FROM cart_consumptions
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY next_attempt_at LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING `+consumptionColumns, limit, claimLease.Seconds())
}

// ListStaleHeld returns held tasks older than olderThan — checkouts that
// never reported success or failure (process crash).
func (r *CartConsumptionRepository) ListStaleHeld(ctx context.Context, olderThan time.Time, limit int) ([]*domain.CartConsumption, error) {
	return r.query(ctx, `SELECT `+consumptionColumns+` FROM cart_consumptions
		WHERE status = 'held' AND created_at < $1 ORDER BY created_at LIMIT $2`, olderThan, limit)
}

func (r *CartConsumptionRepository) Stats(ctx context.Context) (domain.CartConsumptionStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var s domain.CartConsumptionStats
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'held'),
		       count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status = 'parked'),
		       min(created_at) FILTER (WHERE status = 'pending')
		FROM cart_consumptions WHERE status IN ('held', 'pending', 'parked')`).Scan(&s.Held, &s.Pending, &s.Parked, &s.OldestPending)
	return s, err
}
