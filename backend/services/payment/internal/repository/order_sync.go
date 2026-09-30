package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
)

// OrderSync delivers captured/failed outcomes to Order from the
// payment_order_sync outbox, which a trigger fills in the same transaction
// as the intent's status change. It is the only path that tells Order.
type OrderSync struct{ Pool *pgxpool.Pool }

var ErrNoOrderSync = errors.New("no payment outcome to sync")

// maxOrderSyncAttempts: with the 10-minute backoff cap this keeps retrying
// an unreachable Order for about four hours before asking for review.
const maxOrderSyncAttempts = 30

// OrderOutcome is one payment outcome for Order. A capture carries the
// intent id, amount and currency so Order can check them against its own
// snapshot before paying the order.
type OrderOutcome struct {
	PaymentID string
	OrderID   string
	Outcome   string
	Amount    int64
	Currency  string
	Reason    string
}

// Dispatch delivers the next due outcome.
func (s OrderSync) Dispatch(ctx context.Context, deliver func(context.Context, OrderOutcome) error) error {
	return s.dispatch(ctx, "", deliver)
}

// DispatchIntent delivers one intent's outcome now, if it is due; used right
// after a capture commits so the buyer does not wait for the next tick.
func (s OrderSync) DispatchIntent(ctx context.Context, intentID string, deliver func(context.Context, OrderOutcome) error) error {
	return s.dispatch(ctx, intentID, deliver)
}

func (s OrderSync) dispatch(ctx context.Context, intentID string, deliver func(context.Context, OrderOutcome) error) error {
	return inTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var out OrderOutcome
		var reason *string
		err := tx.QueryRow(ctx, `
			SELECT s.payment_intent_id, p.order_id, s.outcome, p.amount, p.currency, p.failure_reason
			FROM payment_order_sync s JOIN payment_intents p ON p.id = s.payment_intent_id
			WHERE s.delivered_at IS NULL AND NOT s.requires_review AND s.next_attempt_at <= now() AND s.attempts < $2
			  AND ($1 = '' OR s.payment_intent_id::text = $1)
			ORDER BY s.next_attempt_at, s.payment_intent_id LIMIT 1
			FOR UPDATE OF s SKIP LOCKED`, intentID, maxOrderSyncAttempts).
			Scan(&out.PaymentID, &out.OrderID, &out.Outcome, &out.Amount, &out.Currency, &reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoOrderSync
		}
		if err != nil {
			return err
		}
		if reason != nil {
			out.Reason = *reason
		}
		deliveryErr := deliver(ctx, out)
		if deliveryErr == nil {
			_, err = tx.Exec(ctx, `UPDATE payment_order_sync SET delivered_at = now(), last_error = NULL WHERE payment_intent_id = $1`, out.PaymentID)
			return err
		}
		var app *apperror.Error
		review := errors.As(deliveryErr, &app) && app.Code == apperror.CodeConflict
		_, err = tx.Exec(ctx, `
			UPDATE payment_order_sync SET attempts = attempts + 1, requires_review = $2 OR attempts + 1 >= $3,
				last_error = CASE WHEN $2 THEN 'order_rejected_outcome' ELSE 'order_delivery_failed' END,
				next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
			WHERE payment_intent_id = $1`, out.PaymentID, review, maxOrderSyncAttempts)
		return err
	})
}

// Requeue sends an outcome again after review (admin retry).
func (s OrderSync) Requeue(ctx context.Context, intentID string) error {
	tag, err := connection(ctx, s.Pool).Exec(ctx, `
		UPDATE payment_order_sync SET requires_review = false, attempts = 0, next_attempt_at = now(), last_error = NULL
		WHERE payment_intent_id = $1 AND delivered_at IS NULL`, intentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// SyncItem is an undelivered outcome needing attention.
type SyncItem struct {
	PaymentIntentID string     `json:"payment_intent_id"`
	OrderID         string     `json:"order_id"`
	Outcome         string     `json:"outcome"`
	Attempts        int        `json:"attempts"`
	RequiresReview  bool       `json:"requires_review"`
	LastError       *string    `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	NextAttemptAt   *time.Time `json:"next_attempt_at,omitempty"`
}

// ListProblems lists outcomes Order refused or that keep failing.
func (s OrderSync) ListProblems(ctx context.Context, limit int) ([]SyncItem, error) {
	rows, err := connection(ctx, s.Pool).Query(ctx, `
		SELECT s.payment_intent_id, p.order_id, s.outcome, s.attempts, s.requires_review, s.last_error, s.created_at, s.next_attempt_at
		FROM payment_order_sync s JOIN payment_intents p ON p.id = s.payment_intent_id
		WHERE s.delivered_at IS NULL AND (s.requires_review OR s.attempts >= 3)
		ORDER BY s.created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncItem{}
	for rows.Next() {
		var it SyncItem
		if err := rows.Scan(&it.PaymentIntentID, &it.OrderID, &it.Outcome, &it.Attempts, &it.RequiresReview, &it.LastError, &it.CreatedAt, &it.NextAttemptAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Counts reports the outbox backlog.
func (s OrderSync) Counts(ctx context.Context) (pending, review int64, err error) {
	err = connection(ctx, s.Pool).QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE NOT requires_review), count(*) FILTER (WHERE requires_review)
		FROM payment_order_sync WHERE delivered_at IS NULL`).Scan(&pending, &review)
	return pending, review, err
}

// Run delivers due outcomes every few seconds, or as soon as wake fires.
func (s OrderSync) Run(ctx context.Context, deliver func(context.Context, OrderOutcome) error, log zerolog.Logger, wake <-chan struct{}) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			err := s.Dispatch(ctx, deliver)
			if errors.Is(err, ErrNoOrderSync) {
				break
			}
			if err != nil {
				log.Error().Err(err).Msg("payment_order_sync_failed")
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-wake:
		}
	}
}
