package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
)

// OrderOutbox delivers fulfillment facts to Order. A row is written in the
// same transaction as the status change and retried until Order answers;
// Order refusing one (4xx) parks it for review.
type OrderOutbox struct{ Pool *pgxpool.Pool }

var ErrNothingToDeliver = errors.New("no shipment event to deliver")

const maxOutboxAttempts = 30

// OutboxEvent is one fact for Order.
type OutboxEvent struct {
	ID             string            `json:"event_id"`
	ShipmentID     string            `json:"shipment_id"`
	VendorOrderID  string            `json:"vendor_order_id"`
	Type           domain.OrderEvent `json:"type"`
	OccurredAt     time.Time         `json:"occurred_at"`
	TrackingNumber *string           `json:"tracking_number,omitempty"`
	// Exception facts (AF-04) only.
	AttemptNo      *int    `json:"attempt_no,omitempty"`
	FailedAttempts *int    `json:"failed_attempts,omitempty"`
	Reason         *string `json:"reason,omitempty"`
}

// Enqueue records an event once per shipment and type.
func (o OrderOutbox) Enqueue(ctx context.Context, e OutboxEvent) error {
	_, err := connection(ctx, o.Pool).Exec(ctx, `
		INSERT INTO shipment_outbox (shipment_id, vendor_order_id, event_type, occurred_at, tracking_number, attempt_no, failed_attempts, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (shipment_id, event_type) DO NOTHING`,
		e.ShipmentID, e.VendorOrderID, e.Type, e.OccurredAt, e.TrackingNumber, e.AttemptNo, e.FailedAttempts, e.Reason)
	return err
}

// EnqueueLegacyReturned gives packages returned before AF-04 their
// exception fact, so Order opens a case an admin takes (never an automatic
// refund). One fact per shipment; returns how many were queued.
func (o OrderOutbox) EnqueueLegacyReturned(ctx context.Context, limit int) (int64, error) {
	tag, err := connection(ctx, o.Pool).Exec(ctx, `
		INSERT INTO shipment_outbox (shipment_id, vendor_order_id, event_type, occurred_at, attempt_no, failed_attempts, reason)
		SELECT s.id, s.vendor_order_id, 'exception_returned', COALESCE(s.returned_at, s.updated_at), s.attempt_no, s.failed_attempts,
			'Returned before delivery exceptions were recorded'
		FROM shipments s
		WHERE s.status = 'returned' AND NOT EXISTS (
			SELECT 1 FROM shipment_outbox x WHERE x.shipment_id = s.id AND x.event_type = 'exception_returned')
		ORDER BY s.updated_at LIMIT $1
		ON CONFLICT (shipment_id, event_type) DO NOTHING`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Dispatch delivers the next due event. Shipped is always delivered before
// delivered/returned of the same shipment.
func (o OrderOutbox) Dispatch(ctx context.Context, deliver func(context.Context, OutboxEvent) error) error {
	return inTx(ctx, o.Pool, func(tx pgx.Tx) error {
		var e OutboxEvent
		err := tx.QueryRow(ctx, `
			SELECT x.id, x.shipment_id, x.vendor_order_id, x.event_type, x.occurred_at, x.tracking_number, x.attempt_no, x.failed_attempts, x.reason
			FROM shipment_outbox x
			WHERE x.delivered_at IS NULL AND NOT x.requires_review AND x.next_attempt_at <= now() AND x.attempts < $1
			  AND (x.event_type = 'shipped' OR NOT EXISTS (
			      SELECT 1 FROM shipment_outbox s WHERE s.shipment_id = x.shipment_id AND s.event_type = 'shipped' AND s.delivered_at IS NULL))
			ORDER BY x.next_attempt_at, x.created_at LIMIT 1
			FOR UPDATE OF x SKIP LOCKED`, maxOutboxAttempts).
			Scan(&e.ID, &e.ShipmentID, &e.VendorOrderID, &e.Type, &e.OccurredAt, &e.TrackingNumber, &e.AttemptNo, &e.FailedAttempts, &e.Reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNothingToDeliver
		}
		if err != nil {
			return err
		}
		deliveryErr := deliver(ctx, e)
		if deliveryErr == nil {
			_, err = tx.Exec(ctx, `UPDATE shipment_outbox SET delivered_at = now(), last_error = NULL WHERE id = $1`, e.ID)
			return err
		}
		var app *apperror.Error
		review := errors.As(deliveryErr, &app) && app.Code == apperror.CodeConflict
		_, err = tx.Exec(ctx, `
			UPDATE shipment_outbox SET attempts = attempts + 1, requires_review = $2 OR attempts + 1 >= $3,
				last_error = left($4, 500), next_attempt_at = now() + interval '1 second' * least(600, power(2, attempts))
			WHERE id = $1`, e.ID, review, maxOutboxAttempts, deliveryErr.Error())
		return err
	})
}

// Requeue sends a parked event again (admin retry).
func (o OrderOutbox) Requeue(ctx context.Context, id string) error {
	tag, err := connection(ctx, o.Pool).Exec(ctx, `
		UPDATE shipment_outbox SET requires_review = false, attempts = 0, next_attempt_at = now(), last_error = NULL
		WHERE id = $1 AND delivered_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleState
	}
	return nil
}

// OutboxProblem is an undelivered event needing attention.
type OutboxProblem struct {
	ID             string    `json:"id"`
	ShipmentID     string    `json:"shipment_id"`
	VendorOrderID  string    `json:"vendor_order_id"`
	EventType      string    `json:"event_type"`
	Attempts       int       `json:"attempts"`
	RequiresReview bool      `json:"requires_review"`
	LastError      *string   `json:"last_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func (o OrderOutbox) Problems(ctx context.Context, limit int) ([]OutboxProblem, error) {
	rows, err := connection(ctx, o.Pool).Query(ctx, `
		SELECT id, shipment_id, vendor_order_id, event_type, attempts, requires_review, last_error, created_at FROM shipment_outbox
		WHERE delivered_at IS NULL AND (requires_review OR attempts >= 3) ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboxProblem{}
	for rows.Next() {
		var p OutboxProblem
		if err := rows.Scan(&p.ID, &p.ShipmentID, &p.VendorOrderID, &p.EventType, &p.Attempts, &p.RequiresReview, &p.LastError, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Counts reports the backlog.
func (o OrderOutbox) Counts(ctx context.Context) (pending, review int64, err error) {
	err = connection(ctx, o.Pool).QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT requires_review), count(*) FILTER (WHERE requires_review)
		FROM shipment_outbox WHERE delivered_at IS NULL`).Scan(&pending, &review)
	return pending, review, err
}

// Run delivers due events every few seconds or when woken.
func (o OrderOutbox) Run(ctx context.Context, deliver func(context.Context, OutboxEvent) error, log zerolog.Logger, wake <-chan struct{}) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			err := o.Dispatch(ctx, deliver)
			if errors.Is(err, ErrNothingToDeliver) {
				break
			}
			if err != nil {
				log.Error().Err(err).Msg("shipment_outbox_failed")
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

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
