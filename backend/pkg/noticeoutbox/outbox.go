// Package noticeoutbox is a service's outbox of "tell this person" requests
// that have no order effect to ride on (PW-009): a refund destination the
// buyer must give or fix (Payment), a support request without an order
// (Order). The row is written in the transaction that decided the fact and
// relayed to Notification afterwards; the row id is the event id, so a
// retry is the same notice there. Rows carry references only.
//
// Each service owns its table (same columns, its own migration and allowed
// notice types); Table names it and is a constant of the service, never
// input.
package noticeoutbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// MaxAttempts is how often a notice is relayed before it needs review.
const MaxAttempts = 20

// Notice is one request to relay; ID is the event id.
type Notice struct {
	ID          string
	UserID      string
	Type        string
	ReferenceID string
}

// Execer is the caller's transaction (or pool).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Outbox struct {
	Pool  *pgxpool.Pool
	Table string
}

var errNone = errors.New("no notice to relay")

// Enqueue records a notice in db (the caller's transaction), once per
// dedupKey: a repeated fact does not tell the person twice.
func (o Outbox) Enqueue(ctx context.Context, db Execer, dedupKey, userID, noticeType, referenceID string) error {
	_, err := db.Exec(ctx, `INSERT INTO `+o.Table+` (dedup_key, user_id, notice_type, reference_id)
		VALUES ($1, $2, $3, $4) ON CONFLICT (dedup_key) DO NOTHING`, dedupKey, userID, noticeType, referenceID)
	if err != nil {
		return fmt.Errorf("queue %s notice: %w", noticeType, err)
	}
	return nil
}

// Dispatch relays the next due notice; it is delivered only once deliver
// returned (the broker stored it or Notification accepted it). A failure
// is retried with backoff and needs review after MaxAttempts.
func (o Outbox) Dispatch(ctx context.Context, deliver func(context.Context, Notice) error) error {
	return pgx.BeginFunc(ctx, o.Pool, func(tx pgx.Tx) error {
		var n Notice
		err := tx.QueryRow(ctx, `SELECT id::text, user_id::text, notice_type, reference_id FROM `+o.Table+`
			WHERE delivered_at IS NULL AND NOT requires_review AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&n.ID, &n.UserID, &n.Type, &n.ReferenceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNone
		}
		if err != nil {
			return err
		}
		if deliveryErr := deliver(ctx, n); deliveryErr != nil {
			_, err = tx.Exec(ctx, `UPDATE `+o.Table+` SET attempts = attempts + 1, requires_review = attempts + 1 >= $2,
				last_error = 'notification relay unavailable', next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
				WHERE id = $1`, n.ID, MaxAttempts)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE `+o.Table+` SET delivered_at = now(), last_error = NULL WHERE id = $1`, n.ID)
		return err
	})
}

// Pending counts notices not relayed yet and those needing review.
func (o Outbox) Pending(ctx context.Context) (waiting, review int64, err error) {
	err = o.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT requires_review), count(*) FILTER (WHERE requires_review)
		FROM `+o.Table+` WHERE delivered_at IS NULL`).Scan(&waiting, &review)
	return waiting, review, err
}

// Drain relays due notices, at most limit.
func (o Outbox) Drain(ctx context.Context, limit int, deliver func(context.Context, Notice) error) (int, error) {
	for n := 0; n < limit; n++ {
		if err := o.Dispatch(ctx, deliver); errors.Is(err, errNone) {
			return n, nil
		} else if err != nil {
			return n, err
		}
	}
	return limit, nil
}

// Run relays due notices every 5 seconds until ctx ends; before is run
// first on each tick (a sweep that queues notices), when set.
func (o Outbox) Run(ctx context.Context, before func(context.Context) error, deliver func(context.Context, Notice) error, log zerolog.Logger) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		if before != nil {
			if err := before(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Str("outbox", o.Table).Msg("notice_outbox_sweep_failed")
			}
		}
		if _, err := o.Drain(ctx, 20, deliver); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Str("outbox", o.Table).Msg("notice_outbox_relay_failed")
		}
		if _, review, err := o.Pending(ctx); err == nil && review > 0 {
			log.Warn().Int64("notices", review).Str("outbox", o.Table).Msg("notice_outbox_needs_review")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
