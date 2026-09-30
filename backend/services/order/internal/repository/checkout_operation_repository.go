package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

type CheckoutOperationRepository struct {
	pool *pgxpool.Pool
}

func NewCheckoutOperationRepository(pool *pgxpool.Pool) *CheckoutOperationRepository {
	return &CheckoutOperationRepository{pool: pool}
}

const checkoutOpColumns = `id, buyer_id, idempotency_key, request_hash, status, order_id, error_code, error_message, error_status, expires_at, created_at, updated_at`

func scanCheckoutOp(row pgx.Row) (*domain.CheckoutOperation, error) {
	var o domain.CheckoutOperation
	err := row.Scan(&o.ID, &o.BuyerID, &o.IdempotencyKey, &o.RequestHash, &o.Status, &o.OrderID,
		&o.ErrorCode, &o.ErrorMessage, &o.ErrorStatus, &o.ExpiresAt, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// Begin claims (buyer, key) for a new checkout, or returns the existing
// operation for that key. started is true when the caller owns a fresh (or
// restarted) operation and must run the checkout. An operation whose key
// expired, or that failed for an infrastructure reason with the same
// request, is restarted.
func (r *CheckoutOperationRepository) Begin(ctx context.Context, buyerID, key, hash string, ttl time.Duration) (op *domain.CheckoutOperation, started bool, err error) {
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.pool)
		inserted, err := scanCheckoutOp(q.QueryRow(ctx, `
			INSERT INTO checkout_operations (buyer_id, idempotency_key, request_hash, expires_at)
			VALUES ($1, $2, $3, now() + $4::double precision * interval '1 second')
			ON CONFLICT (buyer_id, idempotency_key) DO NOTHING
			RETURNING `+checkoutOpColumns, buyerID, key, hash, ttl.Seconds()))
		if err != nil {
			return err
		}
		if inserted != nil {
			op, started = inserted, true
			return nil
		}
		existing, err := scanCheckoutOp(q.QueryRow(ctx, `SELECT `+checkoutOpColumns+` FROM checkout_operations
			WHERE buyer_id = $1 AND idempotency_key = $2 FOR UPDATE`, buyerID, key))
		if err != nil || existing == nil {
			if err == nil {
				err = errors.New("checkout operation vanished")
			}
			return err
		}
		restart := existing.ExpiresAt.Before(time.Now()) && existing.Status != domain.CheckoutOpPreparing
		if !restart && existing.RequestHash == hash && existing.Status == domain.CheckoutOpFailed && !domain.ReplayableFailure(existing.StoredError()) {
			restart = true
		}
		if !restart {
			op = existing
			return nil
		}
		restarted, err := scanCheckoutOp(q.QueryRow(ctx, `
			UPDATE checkout_operations SET request_hash = $2, status = 'preparing', order_id = NULL, error_code = NULL,
			    error_message = NULL, error_status = NULL, expires_at = now() + $3::double precision * interval '1 second', updated_at = now()
			WHERE id = $1 RETURNING `+checkoutOpColumns, existing.ID, hash, ttl.Seconds()))
		if err != nil {
			return err
		}
		op, started = restarted, true
		return nil
	})
	return op, started, err
}

// Complete records the order a checkout produced.
func (r *CheckoutOperationRepository) Complete(ctx context.Context, id, orderID string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE checkout_operations SET status = 'completed', order_id = $2, updated_at = now()
		WHERE id = $1 AND status = 'preparing'`, id, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// Fail records why a checkout did not produce a payable order.
func (r *CheckoutOperationRepository) Fail(ctx context.Context, id string, cause *apperror.Error) error {
	code, status := string(cause.Code), cause.Status
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE checkout_operations SET status = 'failed', error_code = $2, error_message = $3,
		error_status = $4, updated_at = now() WHERE id = $1 AND status = 'preparing'`, id, code, cause.Message, status)
	return err
}

func (r *CheckoutOperationRepository) FindByID(ctx context.Context, id string) (*domain.CheckoutOperation, error) {
	return scanCheckoutOp(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+checkoutOpColumns+` FROM checkout_operations WHERE id = $1`, id))
}

// ListStalePreparing returns operations stuck in preparing since before
// olderThan (the request died mid-checkout).
func (r *CheckoutOperationRepository) ListStalePreparing(ctx context.Context, olderThan time.Time, limit int) ([]*domain.CheckoutOperation, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+checkoutOpColumns+` FROM checkout_operations
		WHERE status = 'preparing' AND updated_at < $1 ORDER BY updated_at LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.CheckoutOperation{}
	for rows.Next() {
		op, err := scanCheckoutOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// PurgeExpired deletes finished operations past their key TTL.
func (r *CheckoutOperationRepository) PurgeExpired(ctx context.Context, limit int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM checkout_operations WHERE id IN (
		SELECT id FROM checkout_operations WHERE status <> 'preparing' AND expires_at < now() - interval '1 day'
		ORDER BY expires_at LIMIT $1)`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
