package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
)

// RefundSync delivers resolved refund outcomes to Order until Order
// acknowledges them. Order refusing one (4xx) parks it for review instead
// of retrying forever.
type RefundSync struct{ Pool *pgxpool.Pool }

var ErrNoRefundSync = errors.New("no refund outcome to sync")

const maxRefundSyncAttempts = 20

func (s RefundSync) Dispatch(ctx context.Context, deliver func(context.Context, domain.RefundOutcome) error) error {
	return inTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var out domain.RefundOutcome
		var failure *string
		err := tx.QueryRow(ctx, `
			SELECT r.order_refund_id::text, r.id, r.status, r.amount, r.currency, r.failure_reason
			FROM payment_refund_sync s JOIN payment_refunds r ON r.id = s.payment_refund_id
			WHERE s.delivered_at IS NULL AND NOT s.requires_review AND s.next_attempt_at <= now()
			ORDER BY s.next_attempt_at, s.payment_refund_id LIMIT 1
			FOR UPDATE OF s SKIP LOCKED`).Scan(&out.OrderRefundID, &out.PaymentRefundID, &out.Status, &out.Amount, &out.Currency, &failure)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoRefundSync
		}
		if err != nil {
			return err
		}
		if failure != nil {
			out.FailureReason = *failure
		}
		deliveryErr := deliver(ctx, out)
		if deliveryErr == nil {
			_, err = tx.Exec(ctx, `UPDATE payment_refund_sync SET delivered_at = now(), last_error = NULL WHERE payment_refund_id = $1`, out.PaymentRefundID)
			return err
		}
		var app *apperror.Error
		review := errors.As(deliveryErr, &app) && app.Code == apperror.CodeConflict
		_, err = tx.Exec(ctx, `
			UPDATE payment_refund_sync SET attempts = attempts + 1,
				requires_review = $2 OR attempts + 1 >= $3,
				last_error = CASE WHEN $2 THEN 'order_rejected_outcome' ELSE 'order_delivery_failed' END,
				next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
			WHERE payment_refund_id = $1`, out.PaymentRefundID, review, maxRefundSyncAttempts)
		return err
	})
}

func (s RefundSync) Run(ctx context.Context, deliver func(context.Context, domain.RefundOutcome) error, log zerolog.Logger) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			err := s.Dispatch(ctx, deliver)
			if errors.Is(err, ErrNoRefundSync) {
				break
			}
			if err != nil {
				log.Error().Err(err).Msg("payment_refund_sync_failed")
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Requeue sends a refund outcome again after review (admin retry).
func (s RefundSync) Requeue(ctx context.Context, refundID string) error {
	tag, err := connection(ctx, s.Pool).Exec(ctx, `
		UPDATE payment_refund_sync SET requires_review = false, attempts = 0, next_attempt_at = now(), last_error = NULL
		WHERE payment_refund_id = $1 AND delivered_at IS NULL`, refundID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// RefundSyncItem is an undelivered refund outcome needing attention.
type RefundSyncItem struct {
	PaymentRefundID string    `json:"payment_refund_id"`
	OrderID         string    `json:"order_id"`
	Status          string    `json:"status"`
	Attempts        int       `json:"attempts"`
	RequiresReview  bool      `json:"requires_review"`
	LastError       *string   `json:"last_error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ListProblems lists refund outcomes Order refused or that keep failing.
func (s RefundSync) ListProblems(ctx context.Context, limit int) ([]RefundSyncItem, error) {
	rows, err := connection(ctx, s.Pool).Query(ctx, `
		SELECT s.payment_refund_id, r.order_id, r.status, s.attempts, s.requires_review, s.last_error, s.created_at
		FROM payment_refund_sync s JOIN payment_refunds r ON r.id = s.payment_refund_id
		WHERE s.delivered_at IS NULL AND (s.requires_review OR s.attempts >= 3)
		ORDER BY s.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RefundSyncItem{}
	for rows.Next() {
		var it RefundSyncItem
		if err := rows.Scan(&it.PaymentRefundID, &it.OrderID, &it.Status, &it.Attempts, &it.RequiresReview, &it.LastError, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkRejected puts a refund outcome Order refused up for review, in the
// caller's transaction.
func (s RefundSync) MarkRejected(ctx context.Context, paymentRefundID string) error {
	_, err := connection(ctx, s.Pool).Exec(ctx, `
		UPDATE payment_refund_sync SET requires_review = true, delivered_at = NULL, last_error = 'order_rejected_outcome'
		WHERE payment_refund_id = $1`, paymentRefundID)
	return err
}
