package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/bulkimport"
	"shopee/backend/services/catalog/internal/domain"
)

// BulkOperations reads the shop's products for AF-16 dry-runs and exports
// and records which client_row_id created which product.
type BulkOperations struct{ Pool *pgxpool.Pool }

var ErrImportRefTaken = errors.New("repository: client_row_id already imported")

// ImportStore lets the shared job store join Catalog transactions.
func ImportStore(pool *pgxpool.Pool) bulkimport.Store {
	return bulkimport.Store{
		Conn:     func(ctx context.Context) bulkimport.Querier { return connection(ctx, pool) },
		Transact: Transactions{Pool: pool}.Run,
	}
}

const bulkVariantColumns = `p.id,p.name,p.version,p.price_amount,p.status,true,v.id::text,v.sku,v.price_amount,v.price_version`

func scanTargets(rows pgx.Rows) ([]domain.BulkTarget, error) {
	defer rows.Close()
	out := []domain.BulkTarget{}
	for rows.Next() {
		var t domain.BulkTarget
		if err := rows.Scan(&t.ProductID, &t.ProductName, &t.ProductVersion, &t.ProductPrice, &t.ProductStatus, &t.HasVariants,
			&t.VariantID, &t.SKU, &t.VariantPrice, &t.PriceVersion); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Targets returns the shop's SKUs (by SKU or variant id) and products (by
// id). Rows of other shops are never returned, so a foreign SKU looks
// exactly like a missing one. Ids must already be valid UUIDs.
func (r BulkOperations) Targets(ctx context.Context, vendor string, skus, variantIDs, productIDs []string) ([]domain.BulkTarget, error) {
	q := connection(ctx, r.Pool)
	out := []domain.BulkTarget{}
	if len(skus)+len(variantIDs) > 0 {
		rows, err := q.Query(ctx, `SELECT `+bulkVariantColumns+` FROM product_variants v JOIN products p ON p.id=v.product_id
 WHERE p.vendor_id=$1 AND (v.sku=ANY($2::text[]) OR v.id=ANY($3::uuid[]))`, vendor, skus, variantIDs)
		if err != nil {
			return nil, err
		}
		variants, err := scanTargets(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, variants...)
	}
	if len(productIDs) > 0 {
		rows, err := q.Query(ctx, `SELECT p.id,p.name,p.version,p.price_amount,p.status,EXISTS(SELECT 1 FROM product_variants x WHERE x.product_id=p.id),'','',0::bigint,0::bigint
 FROM products p WHERE p.vendor_id=$1 AND p.id=ANY($2::uuid[])`, vendor, productIDs)
		if err != nil {
			return nil, err
		}
		products, err := scanTargets(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, products...)
	}
	return out, nil
}

// PriceRows pages the shop's sellable price points (one per SKU, or the
// product price when it has no SKUs) in a stable order for exports.
func (r BulkOperations) PriceRows(ctx context.Context, vendor, afterProduct, afterVariant string, limit int) ([]domain.BulkTarget, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT p.id,p.name,p.version,p.price_amount,p.status,v.id IS NOT NULL,coalesce(v.id::text,''),coalesce(v.sku,''),
 coalesce(v.price_amount,0),coalesce(v.price_version,0)
 FROM products p LEFT JOIN product_variants v ON v.product_id=p.id
 WHERE p.vendor_id=$1 AND (p.id::text,coalesce(v.id::text,''))>($2,$3)
 ORDER BY p.id::text,coalesce(v.id::text,'') LIMIT $4`, vendor, afterProduct, afterVariant, limit)
	if err != nil {
		return nil, err
	}
	return scanTargets(rows)
}

// ImportRefs returns the products already created for client_row_ids.
func (r BulkOperations) ImportRefs(ctx context.Context, vendor string, ids []string) (map[string]string, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT client_row_id,product_id FROM product_import_refs WHERE vendor_id=$1 AND client_row_id=ANY($2::text[])`, vendor, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, product string
		if err = rows.Scan(&id, &product); err != nil {
			return nil, err
		}
		out[id] = product
	}
	return out, rows.Err()
}

// SaveImportRef runs in the transaction that created the product.
func (r BulkOperations) SaveImportRef(ctx context.Context, vendor, clientRowID, productID, jobID string, rowNo int) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO product_import_refs(vendor_id,client_row_id,product_id,job_id,row_no) VALUES($1,$2,$3,$4,$5)`,
		vendor, clientRowID, productID, jobID, rowNo)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrImportRefTaken
	}
	return err
}
