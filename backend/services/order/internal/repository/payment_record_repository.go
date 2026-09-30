package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

// PaymentRecordRepository is Order's ledger of captures Payment reported.
type PaymentRecordRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentRecordRepository(pool *pgxpool.Pool) *PaymentRecordRepository {
	return &PaymentRecordRepository{pool: pool}
}

const paymentColumns = `payment_id, order_id, amount, currency, outcome, rejection_reason, received_at`

func scanPayment(row pgx.Row) (*domain.OrderPayment, error) {
	var p domain.OrderPayment
	err := row.Scan(&p.PaymentID, &p.OrderID, &p.Amount, &p.Currency, &p.Outcome, &p.RejectionReason, &p.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PaymentRecordRepository) Find(ctx context.Context, paymentID string) (*domain.OrderPayment, error) {
	return scanPayment(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+paymentColumns+` FROM order_payments WHERE payment_id = $1`, paymentID))
}

func (r *PaymentRecordRepository) Insert(ctx context.Context, p *domain.OrderPayment) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO order_payments (payment_id, order_id, amount, currency, outcome, rejection_reason)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING received_at`,
		p.PaymentID, p.OrderID, p.Amount, p.Currency, p.Outcome, p.RejectionReason).Scan(&p.ReceivedAt)
}

func (r *PaymentRecordRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.OrderPayment, error) {
	return r.list(ctx, `SELECT `+paymentColumns+` FROM order_payments WHERE order_id = $1 ORDER BY received_at`, orderID)
}

// ListRejected returns captures that could not pay for their order and
// still have no open refund: money waiting to be returned or reviewed.
func (r *PaymentRecordRepository) ListRejected(ctx context.Context, limit, offset int) ([]*domain.OrderPayment, error) {
	return r.list(ctx, `SELECT p.payment_id, p.order_id, p.amount, p.currency, p.outcome, p.rejection_reason, p.received_at
		FROM order_payments p
		WHERE p.outcome = 'rejected' AND NOT EXISTS (
			SELECT 1 FROM order_refunds f WHERE f.payment_id = p.payment_id AND f.status IN ('requested', 'submitted', 'succeeded'))
		ORDER BY p.received_at LIMIT $1 OFFSET $2`, limit, offset)
}

func (r *PaymentRecordRepository) list(ctx context.Context, q string, args ...any) ([]*domain.OrderPayment, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.OrderPayment{}
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
