package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// VendorNotices is the outbox of payout results to tell shops about
// (AF-08): written with the result, relayed to the event bus by Run.
type VendorNotices struct{ Pool *pgxpool.Pool }

var ErrNoVendorNotice = errors.New("no vendor notice to relay")

const maxVendorNoticeAttempts = 20

// VendorNotice is one result to relay; ID is the event id.
type VendorNotice struct {
	ID           string
	VendorID     string
	PayoutItemID string
	Outcome      string
}

// Enqueue records the notice in the caller's transaction, once per payout
// item and outcome.
func (n VendorNotices) Enqueue(ctx context.Context, vendorID, payoutItemID, outcome string) error {
	_, err := connection(ctx, n.Pool).Exec(ctx, `INSERT INTO payment_vendor_notices (vendor_id, payout_item_id, outcome)
		VALUES ($1, $2, $3) ON CONFLICT (payout_item_id, outcome) DO NOTHING`, vendorID, payoutItemID, outcome)
	return err
}

// Dispatch relays the next due notice; delivered only after the broker
// stored it. A failure is retried with backoff and needs review after
// maxVendorNoticeAttempts.
func (n VendorNotices) Dispatch(ctx context.Context, deliver func(context.Context, VendorNotice) error) error {
	return inTx(ctx, n.Pool, func(tx pgx.Tx) error {
		var v VendorNotice
		err := tx.QueryRow(ctx, `SELECT id::text, vendor_id::text, payout_item_id::text, outcome FROM payment_vendor_notices
			WHERE delivered_at IS NULL AND NOT requires_review AND next_attempt_at <= now()
			ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&v.ID, &v.VendorID, &v.PayoutItemID, &v.Outcome)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoVendorNotice
		}
		if err != nil {
			return err
		}
		if deliveryErr := deliver(ctx, v); deliveryErr != nil {
			_, err = tx.Exec(ctx, `UPDATE payment_vendor_notices SET attempts = attempts + 1, requires_review = attempts + 1 >= $2,
				last_error = 'event bus unavailable', next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
				WHERE id = $1`, v.ID, maxVendorNoticeAttempts)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payment_vendor_notices SET delivered_at = now(), last_error = NULL WHERE id = $1`, v.ID)
		return err
	})
}

// Pending counts notices not relayed yet and those needing review.
func (n VendorNotices) Pending(ctx context.Context) (waiting, review int64, err error) {
	err = n.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT requires_review), count(*) FILTER (WHERE requires_review)
		FROM payment_vendor_notices WHERE delivered_at IS NULL`).Scan(&waiting, &review)
	return waiting, review, err
}

// Run relays due notices every 5 seconds until ctx ends.
func (n VendorNotices) Run(ctx context.Context, deliver func(context.Context, VendorNotice) error, log zerolog.Logger) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			err := n.Dispatch(ctx, deliver)
			if errors.Is(err, ErrNoVendorNotice) {
				break
			}
			if err != nil {
				log.Error().Err(err).Msg("payment_vendor_notice_relay_failed")
				break
			}
		}
		if _, review, err := n.Pending(ctx); err == nil && review > 0 {
			log.Warn().Int64("notices", review).Msg("payment_vendor_notices_need_review")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
