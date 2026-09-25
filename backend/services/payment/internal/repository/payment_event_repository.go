package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentEventRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentEventRepository(pool *pgxpool.Pool) *PaymentEventRepository {
	return &PaymentEventRepository{pool: pool}
}

// RecordIfNew inserts providerEventID and reports whether this is the first
// time it's been seen. The unique index on provider_event_id is the actual
// dedup guarantee — this only translates its constraint violation into a
// plain boolean instead of an error, since a retried webhook delivery
// hitting it is an expected, routine occurrence, not a failure.
func (r *PaymentEventRepository) RecordIfNew(ctx context.Context, providerEventID, paymentIntentID, eventType string) (bool, error) {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO payment_events (provider_event_id, payment_intent_id, event_type) VALUES ($1, $2, $3)`,
		providerEventID, paymentIntentID, eventType,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
