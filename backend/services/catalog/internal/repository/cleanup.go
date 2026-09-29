package repository

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"strings"
	"time"
)

type ObjectCleanup struct{ Pool *pgxpool.Pool }

func validObjectKey(key, productID string) bool {
	return strings.HasPrefix(key, "products/"+productID+"/") && !strings.Contains(key, "..")
}

// Track commits before upload so crashes and failed metadata writes leave a durable cleanup record.
func (r ObjectCleanup) Track(ctx context.Context, key, productID string) error {
	if !validObjectKey(key, productID) {
		return fmt.Errorf("invalid catalog object key")
	}
	_, err := r.Pool.Exec(ctx, `INSERT INTO catalog_object_cleanup(object_key,product_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, productID)
	return err
}

// Enqueue shares the metadata transaction when an existing attachment is removed.
func (r ObjectCleanup) Enqueue(ctx context.Context, key, productID string) error {
	if !validObjectKey(key, productID) {
		return fmt.Errorf("invalid catalog object key")
	}
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO catalog_object_cleanup(object_key,product_id) VALUES($1,$2)
 ON CONFLICT(object_key) DO UPDATE SET next_attempt_at=now()+interval '24 hours',attempts=0,last_error=NULL`, key, productID)
	return err
}

type ObjectDeleter interface {
	Delete(context.Context, string) error
}

func (r ObjectCleanup) Sweep(ctx context.Context, store ObjectDeleter) error {
	rows, err := r.Pool.Query(ctx, `SELECT object_key,product_id::text FROM catalog_object_cleanup WHERE next_attempt_at<=now() AND attempts<10 ORDER BY next_attempt_at,object_key LIMIT 20`)
	if err != nil {
		return err
	}
	type candidate struct{ key, product string }
	var items []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.key, &c.product); err != nil {
			rows.Close()
			return err
		}
		items = append(items, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		err = (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
			q := connection(ctx, r.Pool)
			// Writers lock the same product before attaching media, preventing deletion during an attachment.
			var id string
			if err := q.QueryRow(ctx, `SELECT id FROM products WHERE id=$1 FOR UPDATE`, item.product).Scan(&id); err != nil {
				return err
			}
			var referenced bool
			if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images WHERE object_key=$1 UNION ALL SELECT 1 FROM product_media WHERE object_key=$1)`, item.key).Scan(&referenced); err != nil {
				return err
			}
			if !referenced {
				deletionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				deleteErr := store.Delete(deletionCtx, item.key)
				cancel()
				if deleteErr != nil {
					_, err := q.Exec(ctx, `UPDATE catalog_object_cleanup SET attempts=attempts+1,last_error='object_delete_failed',next_attempt_at=now()+least(interval '1 hour',interval '1 minute'*power(2,attempts)) WHERE object_key=$1`, item.key)
					return err
				}
			}
			_, err := q.Exec(ctx, `DELETE FROM catalog_object_cleanup WHERE object_key=$1`, item.key)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r ObjectCleanup) Run(ctx context.Context, store ObjectDeleter, log zerolog.Logger) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := r.Sweep(ctx, store); err != nil {
			log.Error().Err(err).Msg("catalog_cleanup_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
