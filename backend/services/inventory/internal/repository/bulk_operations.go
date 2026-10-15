package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bulkimport"
	"shopee/backend/services/inventory/internal/domain"
)

// ImportStore lets the shared job store join Inventory transactions.
func ImportStore(pool *pgxpool.Pool) bulkimport.Store {
	return bulkimport.Store{
		Conn:     func(ctx context.Context) bulkimport.Querier { return connection(ctx, pool) },
		Transact: Transactions{Pool: pool}.Run,
	}
}

// BulkItems reads a shop's stock items for AF-16 dry-runs and exports.
type BulkItems struct{ Pool *pgxpool.Pool }

const bulkItemColumns = `id,product_id,variant_id,vendor_id,available_quantity,reserved_quantity,created_at,updated_at,version,low_stock_threshold,alert_enabled`

func (r BulkItems) scan(ctx context.Context, sql string, args ...any) ([]domain.InventoryItem, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.InventoryItem
	for rows.Next() {
		var i domain.InventoryItem
		if err = rows.Scan(&i.ID, &i.ProductID, &i.VariantID, &i.VendorID, &i.AvailableQuantity, &i.ReservedQuantity, &i.CreatedAt, &i.UpdatedAt,
			&i.Version, &i.LowStockThreshold, &i.AlertEnabled); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ForTargets returns the shop's items of whole products (no SKUs) and of
// SKUs. Ids must already be valid UUIDs.
func (r BulkItems) ForTargets(ctx context.Context, vendor string, productIDs, variantIDs []string) ([]domain.InventoryItem, error) {
	return r.scan(ctx, `SELECT `+bulkItemColumns+` FROM inventory_items WHERE vendor_id=$1 AND
 ((variant_id IS NULL AND product_id=ANY($2::uuid[])) OR variant_id=ANY($3::uuid[]))`, vendor, productIDs, variantIDs)
}

// After pages a shop's items by id for exports.
func (r BulkItems) After(ctx context.Context, vendor, after string, limit int) ([]domain.InventoryItem, error) {
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	}
	return r.scan(ctx, `SELECT `+bulkItemColumns+` FROM inventory_items WHERE vendor_id=$1 AND id>$2::uuid ORDER BY id LIMIT $3`, vendor, after, limit)
}
