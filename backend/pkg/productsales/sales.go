package productsales

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"sort"
	"time"
)

type Status struct {
	ProductID string `json:"product_id"`
	Version   int64  `json:"version"`
	Visible   bool   `json:"is_visible"`
}
type Store struct{ Pool *pgxpool.Pool }

func (s Store) Apply(ctx context.Context, v Status) error {
	if _, err := uuid.Parse(v.ProductID); err != nil || v.Version < 1 {
		return apperror.Validation("Invalid product status event")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `INSERT INTO product_sale_status(product_id,version,is_visible) VALUES($1,$2,$3)
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
