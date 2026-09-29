package repository

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/productsales"
	"time"
)

var ErrNoProductStatus = errors.New("no due product status")

type StatusOutbox struct {
	Pool    *pgxpool.Pool
	Publish func(context.Context, productsales.Status) error
}

// Backfill resumes from products whose current version has not yet been acknowledged.
func (r StatusOutbox) Backfill(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.Pool.Exec(ctx, `INSERT INTO product_status_outbox(product_id)
 SELECT p.id FROM products p WHERE p.enforced_version<p.version AND NOT EXISTS(SELECT 1 FROM product_status_outbox o WHERE o.product_id=p.id)
 ORDER BY p.id LIMIT 100 ON CONFLICT DO NOTHING`)
	return err
}
func (r StatusOutbox) Dispatch(ctx context.Context) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.Pool)
		var v productsales.Status
		err := q.QueryRow(ctx, `SELECT p.id,p.version,p.status='approved' AND p.is_active FROM products p JOIN product_status_outbox o ON o.product_id=p.id
  WHERE o.next_attempt_at<=now() AND o.attempts<10 ORDER BY o.next_attempt_at,p.id LIMIT 1 FOR UPDATE OF p,o SKIP LOCKED`).Scan(&v.ProductID, &v.Version, &v.Visible)
		if err == pgx.ErrNoRows {
			return ErrNoProductStatus
		}
		if err != nil {
			return err
		}
		sendErr := r.Publish(ctx, v)
		if sendErr != nil {
			_, err = q.Exec(ctx, `UPDATE product_status_outbox SET attempts=attempts+1,last_error='order_delivery_failed',next_attempt_at=now()+least(interval '5 minutes',interval '1 second'*power(2,attempts)) WHERE product_id=$1`, v.ProductID)
			return err
		}
		if _, err = q.Exec(ctx, `UPDATE products SET enforced_version=$2 WHERE id=$1 AND version=$2`, v.ProductID, v.Version); err != nil {
			return err
		}
		_, err = q.Exec(ctx, `DELETE FROM product_status_outbox WHERE product_id=$1`, v.ProductID)
		return err
	})
}
func (r StatusOutbox) Run(ctx context.Context, log zerolog.Logger) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if err := r.Backfill(ctx); err != nil {
			log.Error().Err(err).Msg("catalog_status_backfill_failed")
		}
		for i := 0; i < 100 && ctx.Err() == nil; i++ {
			if err := r.Dispatch(ctx); err != nil {
				if errors.Is(err, ErrNoProductStatus) {
					break
				}
				log.Error().Err(err).Msg("catalog_status_dispatch_failed")
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
