package adapter

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/productsales"
)

// BusProductStatusPublisher publishes catalog.product_status_changed; the
// event id names the product version and visibility it reports.
func BusProductStatusPublisher(bus *eventbus.Bus) func(context.Context, productsales.Status) error {
	return func(ctx context.Context, v productsales.Status) error {
		id := fmt.Sprintf("product-%s-v%d-%t", v.ProductID, v.Version, v.Visible)
		env, err := events.ProductStatus(id, v)
		if err != nil {
			return err
		}
		return bus.Publish(ctx, env.WithCorrelation(""))
	}
}

// StockChangedHandler drops the storefront stock cache of the variants an
// inventory.stock_changed event names (Catalog reads stock again on the
// next view).
func StockChangedHandler() eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var change events.StockChange
		if err := env.Decode(&change); err != nil {
			return err
		}
		if len(change.VariantIDs) == 0 || len(change.VariantIDs) > 100 {
			return apperror.Validation("A stock change names 1 to 100 variants")
		}
		for _, id := range change.VariantIDs {
			if _, err := uuid.Parse(id); err != nil {
				return apperror.Validation("Invalid variant id in stock change")
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM variant_stock_cache WHERE variant_id = ANY($1)`, change.VariantIDs)
		return err
	}
}
