package repository

import (
	"context"
	"errors"
	"fmt"
	"shopee/backend/pkg/casesla"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
)

var ErrRefundNotFound = errors.New("repository: refund not found")

const refundColumns = `id, payment_intent_id, order_id, order_refund_id, vendor_order_id, amount, currency, reason, status, requested_by,
	evidence_reference, note, failure_reason, resolved_by, resolved_at, created_at, updated_at`

type RefundRepository struct{ pool *pgxpool.Pool }

func NewRefundRepository(pool *pgxpool.Pool) *RefundRepository { return &RefundRepository{pool: pool} }

func scanRefund(row pgx.Row) (*domain.Refund, error) {
	var r domain.Refund
	err := row.Scan(&r.ID, &r.PaymentIntentID, &r.OrderID, &r.OrderRefundID, &r.VendorOrderID, &r.Amount, &r.Currency, &r.Reason, &r.Status, &r.RequestedBy,
		&r.EvidenceReference, &r.Note, &r.FailureReason, &r.ResolvedBy, &r.ResolvedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundNotFound
	}
	return &r, err
}

// Request records Order's refund request exactly once. The intent row is
// locked so concurrent refunds against one capture are judged one at a
// time by check, which receives the intent and the sum of its non-failed
// refunds. A replay of the same OrderRefundID returns the stored refund
// with created=false; a different request under that id is a conflict.
func (r *RefundRepository) Request(ctx context.Context, req domain.RefundRequest,
	check func(intent *domain.PaymentIntent, committed int64) error) (refund *domain.Refund, created bool, err error) {
	err = inTx(ctx, r.pool, func(tx pgx.Tx) error {
		existing, err := scanRefund(tx.QueryRow(ctx, `SELECT `+refundColumns+` FROM payment_refunds WHERE order_refund_id = $1`, req.OrderRefundID))
		if err == nil {
			if !existing.Same(req) {
				return apperror.Conflict("This refund id was already used for a different refund")
			}
			refund = existing
			return nil
		}
		if !errors.Is(err, ErrRefundNotFound) {
			return err
		}
		intent, err := lockRefundIntent(ctx, tx, req)
		if err != nil {
			return err
		}
		var committed int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM payment_refunds WHERE payment_intent_id = $1 AND status <> 'failed'`, intent.ID).Scan(&committed); err != nil {
			return err
		}
		if err := check(intent, committed); err != nil {
			return err
		}
		refund, err = scanRefund(tx.QueryRow(ctx, `
			INSERT INTO payment_refunds (payment_intent_id, order_id, order_refund_id, vendor_order_id, amount, currency, reason, status, requested_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING `+refundColumns,
			intent.ID, req.OrderID, req.OrderRefundID, req.VendorOrderID, req.Amount, req.Currency, req.Reason, domain.RefundAwaitingProvider, req.RequestedBy))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return apperror.Conflict("This refund was submitted concurrently; retry")
			}
			return err
		}
		created = true
		_, err = casesla.Sync(ctx, tx, refund.SLAStage())
		return err
	})
	return refund, created, err
}

// lockRefundIntent picks the capture a refund draws on: the pinned payment,
// or the order's only captured intent. Several captures without a pin are
// ambiguous and refused rather than guessed.
func lockRefundIntent(ctx context.Context, tx pgx.Tx, req domain.RefundRequest) (*domain.PaymentIntent, error) {
	if req.PaymentID != nil {
		intent, err := scanPaymentIntent(tx.QueryRow(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1 AND order_id = $2 FOR UPDATE`, *req.PaymentID, req.OrderID))
		if errors.Is(err, ErrPaymentIntentNotFound) {
			return nil, apperror.NotFound("Payment not found for this order")
		}
		return intent, err
	}
	rows, err := tx.Query(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE order_id = $1 AND status IN ('captured', 'refunded') ORDER BY id FOR UPDATE`, req.OrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []*domain.PaymentIntent
	for rows.Next() {
		intent, err := scanPaymentIntent(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, intent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, apperror.Conflict("This order has no captured payment to refund")
	case 1:
		return found[0], nil
	default:
		return nil, apperror.Conflict("This order has several captured payments; the refund must name one")
	}
}

func (r *RefundRepository) FindByID(ctx context.Context, id string) (*domain.Refund, error) {
	return scanRefund(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+refundColumns+` FROM payment_refunds WHERE id = $1`, id))
}

// List returns refunds newest first; status "open" means not yet resolved.
func (r *RefundRepository) List(ctx context.Context, status string, limit, offset int) ([]*domain.Refund, int, error) {
	where, args := "", []any{}
	switch status {
	case "":
	case "open":
		where = ` WHERE status IN ('awaiting_provider_refund', 'pending')`
	default:
		where, args = ` WHERE status = $1`, append(args, status)
	}
	var total int
	if err := connection(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM payment_refunds`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := connection(ctx, r.pool).Query(ctx, fmt.Sprintf(`SELECT %s FROM payment_refunds%s ORDER BY created_at DESC, id LIMIT $%d OFFSET $%d`,
		refundColumns, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*domain.Refund{}
	for rows.Next() {
		refund, err := scanRefund(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, refund)
	}
	return out, total, rows.Err()
}

// Resolve locks the refund, lets resolve apply the domain decision, and in
// the same transaction persists it, queues the outcome for Order and marks
// the intent refunded once succeeded refunds cover the whole capture.
func (r *RefundRepository) Resolve(ctx context.Context, id string, resolve func(*domain.Refund) (bool, error)) (refund *domain.Refund, err error) {
	err = inTx(ctx, r.pool, func(tx pgx.Tx) error {
		refund, err = scanRefund(tx.QueryRow(ctx, `SELECT `+refundColumns+` FROM payment_refunds WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		changed, err := resolve(refund)
		if err != nil || !changed {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payment_refunds SET status = $2, evidence_reference = $3, note = $4, failure_reason = $5,
				resolved_by = $6, resolved_at = $7, updated_at = now()
			WHERE id = $1`, refund.ID, refund.Status, refund.EvidenceReference, refund.Note, refund.FailureReason, refund.ResolvedBy, refund.ResolvedAt); err != nil {
			return err
		}
		if _, err = casesla.Sync(ctx, tx, refund.SLAStage()); err != nil {
			return err
		}
		if refund.OrderRefundID != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO payment_refund_sync (payment_refund_id) VALUES ($1) ON CONFLICT DO NOTHING`, refund.ID); err != nil {
				return err
			}
		}
		if refund.Status == domain.RefundSucceeded {
			_, err := tx.Exec(ctx, `
				UPDATE payment_intents p SET status = 'refunded', updated_at = now()
				WHERE p.id = $1 AND p.status = 'captured'
				  AND p.amount <= (SELECT COALESCE(SUM(amount), 0) FROM payment_refunds WHERE payment_intent_id = p.id AND status = 'succeeded')`,
				refund.PaymentIntentID)
			return err
		}
		return nil
	})
	return refund, err
}

// Search finds refunds by id, order, Order's refund id or payment intent.
func (r *RefundRepository) Search(ctx context.Context, q string, limit int) ([]*domain.Refund, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+refundColumns+` FROM payment_refunds
		WHERE id::text = $1 OR order_id::text = $1 OR order_refund_id::text = $1 OR payment_intent_id::text = $1
		ORDER BY created_at DESC LIMIT $2`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Refund{}
	for rows.Next() {
		refund, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, refund)
	}
	return out, rows.Err()
}
