package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

type RefundRepository struct {
	pool *pgxpool.Pool
}

func NewRefundRepository(pool *pgxpool.Pool) *RefundRepository {
	return &RefundRepository{pool: pool}
}

var ErrRefundNotFound = errors.New("repository: refund not found")

const refundColumns = `id, order_id, vendor_order_id, return_request_id, payment_id, reason_code, amount, currency, reason, status,
	payment_refund_id, failure_reason, requested_by, created_at, updated_at, resolved_at`

func scanRefund(row pgx.Row) (*domain.Refund, error) {
	var f domain.Refund
	err := row.Scan(&f.ID, &f.OrderID, &f.VendorOrderID, &f.ReturnRequestID, &f.PaymentID, &f.ReasonCode, &f.Amount, &f.Currency,
		&f.Reason, &f.Status, &f.PaymentRefundID, &f.FailureReason, &f.RequestedBy, &f.CreatedAt, &f.UpdatedAt, &f.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundNotFound
	}
	return &f, err
}

// Create inserts a refund in the caller's transaction. A second open refund
// for the same return or rejected capture is refused by a unique index.
func (r *RefundRepository) Create(ctx context.Context, f *domain.Refund) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO order_refunds (order_id, vendor_order_id, return_request_id, payment_id, reason_code, amount, currency, reason, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, status, created_at, updated_at`,
		f.OrderID, f.VendorOrderID, f.ReturnRequestID, f.PaymentID, f.ReasonCode, f.Amount, f.Currency, f.Reason, f.RequestedBy,
	).Scan(&f.ID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apperror.Conflict("A refund is already open for this return or payment")
	}
	return err
}

func (r *RefundRepository) FindByID(ctx context.Context, id string) (*domain.Refund, error) {
	return scanRefund(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+refundColumns+` FROM order_refunds WHERE id = $1`, id))
}

// Transition moves a refund to a new status only from the expected one.
func (r *RefundRepository) Transition(ctx context.Context, id string, from, to domain.RefundStatus, paymentRefundID, failure *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE order_refunds SET status = $3, payment_refund_id = COALESCE($4, payment_refund_id), failure_reason = COALESCE($5, failure_reason),
		    resolved_at = CASE WHEN $3 IN ('succeeded', 'failed', 'rejected') THEN now() ELSE resolved_at END, updated_at = now()
		WHERE id = $1 AND status = $2`, id, from, to, paymentRefundID, failure)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// OpenTotals returns the amount already reserved by open or paid refunds of
// the order's applied payment (refunds of rejected captures are separate
// money), for the whole order and for one vendor order ("" = none).
func (r *RefundRepository) OpenTotals(ctx context.Context, orderID, vendorOrderID string) (orderTotal, vendorTotal int64, err error) {
	err = connection(ctx, r.pool).QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0), COALESCE(sum(amount) FILTER (WHERE vendor_order_id::text = $2), 0)
		FROM order_refunds WHERE order_id = $1 AND payment_id IS NULL AND status IN ('requested', 'submitted', 'succeeded')`, orderID, vendorOrderID).
		Scan(&orderTotal, &vendorTotal)
	return
}

func (r *RefundRepository) ListByOrder(ctx context.Context, orderID string) ([]*domain.Refund, error) {
	return r.list(ctx, `SELECT `+refundColumns+` FROM order_refunds WHERE order_id = $1 ORDER BY created_at, id`, orderID)
}

// List returns refunds for admin, optionally by status.
func (r *RefundRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.Refund, error) {
	return r.list(ctx, `SELECT `+refundColumns+` FROM order_refunds WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}

func (r *RefundRepository) list(ctx context.Context, q string, args ...any) ([]*domain.Refund, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Refund{}
	for rows.Next() {
		f, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
