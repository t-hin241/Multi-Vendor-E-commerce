package productsales

import (
	"context"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/httpresponse"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Status struct {
	ProductID string `json:"product_id"`
	Version   int64  `json:"version"`
	Visible   bool   `json:"is_visible"`
}
type Store struct{ Pool *pgxpool.Pool }

func (s Store) Apply(ctx context.Context, v Status) error {
	return applyStatus(ctx, s.Pool, v)
}

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EventHandler applies catalog.product_status_changed events in the inbox
// transaction (an older version than the one held is ignored).
func (s Store) EventHandler() eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var v Status
		if err := env.Decode(&v); err != nil {
			return err
		}
		return applyStatus(ctx, tx, v)
	}
}

func applyStatus(ctx context.Context, q execer, v Status) error {
	if _, err := uuid.Parse(v.ProductID); err != nil || v.Version < 1 {
		return apperror.Validation("Invalid product status event")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := q.Exec(ctx, `INSERT INTO product_sale_status(product_id,version,is_visible) VALUES($1,$2,$3)
 ON CONFLICT(product_id) DO UPDATE SET version=excluded.version,is_visible=excluded.is_visible,confirmed_at=now()
 WHERE product_sale_status.version<excluded.version OR (product_sale_status.version=excluded.version AND product_sale_status.is_visible=excluded.is_visible)`, v.ProductID, v.Version, v.Visible)
	return err
}
func (s Store) Handler(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var v Status
		if err := c.ShouldBindJSON(&v); err != nil {
			httpresponse.HandleError(c, log, apperror.Validation("Invalid product status event"))
			return
		}
		if err := s.Apply(c.Request.Context(), v); err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		httpresponse.OK(c, 200, v)
	}
}

// LockVisible serializes checkout with product unpublish/revision event application.
func LockVisible(ctx context.Context, tx pgx.Tx, versions map[string]int64) error {
	ids := make([]string, 0, len(versions))
	for id := range versions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var visible bool
		var version int64
		err := tx.QueryRow(ctx, `SELECT is_visible,version FROM product_sale_status WHERE product_id=$1 FOR SHARE`, id).Scan(&visible, &version)
		if err == pgx.ErrNoRows {
			return apperror.Conflict("Product is synchronizing; retry checkout")
		}
		if err != nil {
			return err
		}
		if !visible || version != versions[id] {
			return apperror.Conflict("Product changed; refresh checkout")
		}
	}
	return nil
}
