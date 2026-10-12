package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"time"
)

// VariantPriceOutbox publishes references only. Listings currently read prices
// directly from PostgreSQL; a future cache consumer must keep the newest version.
type VariantPriceOutbox struct {
	Pool    *pgxpool.Pool
	Publish func(context.Context, eventbus.Envelope) error
}

func (r VariantPriceOutbox) Dispatch(ctx context.Context) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		var variant, product, request string
		var version, productVersion int64
		var created time.Time
		q := connection(ctx, r.Pool)
		err := q.QueryRow(ctx, `SELECT variant_id,price_version,product_id,product_version,request_id,created_at FROM variant_price_outbox WHERE next_attempt_at<=now() AND attempts<5 ORDER BY next_attempt_at,variant_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&variant, &version, &product, &productVersion, &request, &created)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoProductStatus
		}
		if err != nil {
			return err
		}
		env, err := events.VariantPriceChangedEvent(fmt.Sprintf("variant-%s-v%d", variant, version), events.VariantPriceChange{
			ProductID: product, VariantID: variant, ProductVersion: productVersion, PricingVersion: version,
		})
		if err != nil {
			return err
		}
		env.CorrelationID = request
		env.OccurredAt = created
		if err = r.Publish(ctx, env); err != nil {
			_, saveErr := q.Exec(ctx, `UPDATE variant_price_outbox SET attempts=attempts+1,next_attempt_at=now()+interval '1 second'*(power(2,attempts)+random()) WHERE variant_id=$1 AND price_version=$2`, variant, version)
			return saveErr
		}
		_, err = q.Exec(ctx, `DELETE FROM variant_price_outbox WHERE variant_id=$1 AND price_version=$2`, variant, version)
		return err
	})
}
func (r VariantPriceOutbox) Run(ctx context.Context, log zerolog.Logger) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for range 50 {
			if err := r.Dispatch(ctx); err != nil {
				if !errors.Is(err, ErrNoProductStatus) && ctx.Err() == nil {
					log.Error().Err(err).Msg("variant_price_event_dispatch_failed")
				}
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
