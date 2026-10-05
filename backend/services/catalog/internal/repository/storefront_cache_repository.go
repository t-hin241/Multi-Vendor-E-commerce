package repository

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StorefrontCacheRepository is Catalog's own local read-model of facts
// owned by other services (Vendor's shop names, Order's sold quantities),
// refreshed opportunistically whenever a live cross-service call succeeds.
// It is not the source of truth — it exists purely as a fallback so the
// storefront listing can keep showing a product's last-known vendor name
// and sold count if Vendor or Order is briefly unreachable, instead of
// going blank.
type StorefrontCacheRepository struct {
	pool *pgxpool.Pool
}

func NewStorefrontCacheRepository(pool *pgxpool.Pool) *StorefrontCacheRepository {
	return &StorefrontCacheRepository{pool: pool}
}

func (r *StorefrontCacheRepository) UpsertVendorNames(ctx context.Context, entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	ids, names := splitEntries(entries)
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO vendor_name_cache (vendor_id, shop_name, updated_at)
		SELECT id, name, now() FROM unnest($1::uuid[], $2::text[]) AS t(id, name) ORDER BY id
		ON CONFLICT (vendor_id) DO UPDATE SET shop_name = excluded.shop_name, updated_at = excluded.updated_at`, ids, names)
	return err
}

// splitEntries turns a map into parallel arrays for one unnest() upsert:
// these refreshes run on storefront reads, so one statement per call
// instead of one per row (and one connection round trip per row). Keys are
// sorted and the statements insert in that order: concurrent refreshes of
// overlapping rows then lock them in the same order and cannot deadlock
// (map order is random; the load test showed hundreds of deadlocks).
func splitEntries[V any](entries map[string]V) ([]string, []V) {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := make([]V, 0, len(entries))
	for _, k := range keys {
		values = append(values, entries[k])
	}
	return keys, values
}

func (r *StorefrontCacheRepository) GetVendorNames(ctx context.Context, vendorIDs []string) (map[string]string, error) {
	if len(vendorIDs) == 0 {
		return map[string]string{}, nil
	}
	const query = `SELECT vendor_id, shop_name FROM vendor_name_cache WHERE vendor_id = ANY($1) AND updated_at>now()-interval '15 minutes'`
	rows, err := connection(ctx, r.pool).Query(ctx, query, vendorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string, len(vendorIDs))
	for rows.Next() {
		var vendorID, shopName string
		if err := rows.Scan(&vendorID, &shopName); err != nil {
			return nil, err
		}
		out[vendorID] = shopName
	}
	return out, rows.Err()
}

func (r *StorefrontCacheRepository) UpsertQuantitySold(ctx context.Context, entries map[string]int64) error {
	if len(entries) == 0 {
		return nil
	}
	ids, quantities := splitEntries(entries)
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO product_sales_cache (product_id, quantity_sold, updated_at)
		SELECT id, qty, now() FROM unnest($1::uuid[], $2::bigint[]) AS t(id, qty) ORDER BY id
		ON CONFLICT (product_id) DO UPDATE SET quantity_sold = excluded.quantity_sold, updated_at = excluded.updated_at`, ids, quantities)
	return err
}

func (r *StorefrontCacheRepository) GetQuantitySold(ctx context.Context, productIDs []string) (map[string]int64, error) {
	if len(productIDs) == 0 {
		return map[string]int64{}, nil
	}
	const query = `SELECT product_id, quantity_sold FROM product_sales_cache WHERE product_id = ANY($1) AND updated_at>now()-interval '15 minutes'`
	rows, err := connection(ctx, r.pool).Query(ctx, query, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, len(productIDs))
	for rows.Next() {
		var productID string
		var quantity int64
		if err := rows.Scan(&productID, &quantity); err != nil {
			return nil, err
		}
		out[productID] = quantity
	}
	return out, rows.Err()
}

func (r *StorefrontCacheRepository) UpsertVariantStock(ctx context.Context, entries map[string]int64) error {
	if len(entries) == 0 {
		return nil
	}
	ids, quantities := splitEntries(entries)
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO variant_stock_cache (variant_id, available_quantity, updated_at)
		SELECT id, qty, now() FROM unnest($1::uuid[], $2::bigint[]) AS t(id, qty) ORDER BY id
		ON CONFLICT (variant_id) DO UPDATE SET available_quantity = excluded.available_quantity, updated_at = excluded.updated_at`, ids, quantities)
	return err
}

func (r *StorefrontCacheRepository) GetVariantStock(ctx context.Context, variantIDs []string) (map[string]int64, error) {
	if len(variantIDs) == 0 {
		return map[string]int64{}, nil
	}
	const query = `SELECT variant_id, available_quantity FROM variant_stock_cache WHERE variant_id = ANY($1) AND updated_at>now()-interval '60 seconds'`
	rows, err := connection(ctx, r.pool).Query(ctx, query, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, len(variantIDs))
	for rows.Next() {
		var variantID string
		var qty int64
		if err := rows.Scan(&variantID, &qty); err != nil {
			return nil, err
		}
		out[variantID] = qty
	}
	return out, rows.Err()
}
