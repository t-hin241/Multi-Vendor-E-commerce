package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/catalog/internal/domain"
)

type ProductVariantRepository struct {
	pool *pgxpool.Pool
}

func NewProductVariantRepository(pool *pgxpool.Pool) *ProductVariantRepository {
	return &ProductVariantRepository{pool: pool}
}

var ErrVariantNotFound = errors.New("repository: variant not found")
var ErrSKUTaken = errors.New("repository: sku already taken")
var ErrVariantAlreadyExists = errors.New("repository: a variant with this option combination already exists for the product")

// Create inserts the variant and its option selections in one transaction,
// so a variant never exists with zero or partial options.
func (r *ProductVariantRepository) Create(ctx context.Context, v *domain.ProductVariant, selections []domain.VariantOptionSelection, audit ...string) error {
	tx, err := begin(ctx, r.pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const insertVariant = `
		INSERT INTO product_variants (product_id, sku, variant_key, price_amount,price_source)
		VALUES ($1, $2, $3, $4,'vendor_initial_v1')
		RETURNING id, created_at, price_version`
	if err := tx.QueryRow(ctx, insertVariant, v.ProductID, v.SKU, v.VariantKey, v.PriceAmount).Scan(&v.ID, &v.CreatedAt, &v.PriceVersion); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "product_variants_sku_key" {
				return ErrSKUTaken
			}
			return ErrVariantAlreadyExists
		}
		return err
	}

	const insertOption = `INSERT INTO product_variant_options (variant_id, attribute_id, option_id) VALUES ($1, $2, $3)`
	for _, s := range selections {
		if _, err := tx.Exec(ctx, insertOption, v.ID, s.AttributeID, s.OptionID); err != nil {
			return err
		}
	}

	actor, request := "", ""
	if len(audit) == 2 {
		actor, request = audit[0], audit[1]
	}
	if _, err := tx.Exec(ctx, `INSERT INTO variant_price_audit(product_id,variant_id,actor_user_id,new_price,price_version,source,request_id,membership_version) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,'vendor_initial_v1',$6,$7)`, v.ProductID, v.ID, actor, v.PriceAmount, v.PriceVersion, request, shopaccess.UsedMembershipVersion(ctx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ProductVariantRepository) FindByID(ctx context.Context, id string) (*domain.ProductVariant, error) {
	const query = `SELECT id, product_id, sku, variant_key, price_amount, price_version, created_at FROM product_variants WHERE id = $1`
	var v domain.ProductVariant
	err := connection(ctx, r.pool).QueryRow(ctx, query, id).Scan(&v.ID, &v.ProductID, &v.SKU, &v.VariantKey, &v.PriceAmount, &v.PriceVersion, &v.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, err
	}
	return &v, nil
}

// HasVariantsForProduct reports whether a product has at least one variant
// — used to require a variant selection on add-to-cart/checkout for such
// products, and to skip the variant-related work entirely for the (still
// common) case of a plain product with none.
func (r *ProductVariantRepository) HasVariantsForProduct(ctx context.Context, productID string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM product_variants WHERE product_id = $1)`
	var exists bool
	err := connection(ctx, r.pool).QueryRow(ctx, query, productID).Scan(&exists)
	return exists, err
}

func (r *ProductVariantRepository) ListForProduct(ctx context.Context, productID string) ([]*domain.ProductVariant, error) {
	const query = `SELECT id, product_id, sku, variant_key, price_amount, price_version, created_at FROM product_variants WHERE product_id = $1 ORDER BY created_at ASC`
	rows, err := connection(ctx, r.pool).Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []*domain.ProductVariant
	for rows.Next() {
		var v domain.ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.VariantKey, &v.PriceAmount, &v.PriceVersion, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, &v)
	}
	return variants, rows.Err()
}

// UpdatePrice is a compare-and-set within ProductUseCase's product-row lock.
// The audit row and parent product version fence commit atomically with it.
func (r *ProductVariantRepository) UpdatePrice(ctx context.Context, productID, variantID string, expectedVersion, amount int64, actorID, requestID string) (*domain.ProductVariant, error) {
	if !InTransaction(ctx) {
		return nil, errors.New("variant price update requires a transaction")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var v domain.ProductVariant
	err := connection(ctx, r.pool).QueryRow(ctx, `WITH current AS MATERIALIZED (
 SELECT price_amount FROM product_variants WHERE id=$1 AND product_id=$2 AND price_version=$3 FOR UPDATE
 ), updated AS (
 UPDATE product_variants SET price_amount=$4,price_version=price_version+1,price_source='vendor_revision_v1'
 FROM current WHERE id=$1 AND product_id=$2 AND price_version=$3
 RETURNING id,product_id,sku,variant_key,product_variants.price_amount,price_version,created_at,current.price_amount AS old_price
 ), audited AS (
	 INSERT INTO variant_price_audit(product_id,variant_id,actor_user_id,old_price,new_price,price_version,source,request_id,membership_version)
	 SELECT updated.product_id,updated.id,$5,updated.old_price,updated.price_amount,updated.price_version,'vendor_revision_v1',$6,$7
 FROM updated RETURNING variant_id
 ) SELECT id,product_id,sku,variant_key,price_amount,price_version,created_at FROM updated
	 WHERE EXISTS(SELECT 1 FROM audited)`, variantID, productID, expectedVersion, amount, actorID, requestID, shopaccess.UsedMembershipVersion(ctx)).Scan(&v.ID, &v.ProductID, &v.SKU, &v.VariantKey, &v.PriceAmount, &v.PriceVersion, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVariantNotFound
	}
	if err != nil {
		return nil, err
	}
	_, err = connection(ctx, r.pool).Exec(ctx, `INSERT INTO variant_price_outbox(variant_id,price_version,product_id,product_version,request_id) SELECT $1,$2,id,version,$4 FROM products WHERE id=$3`, v.ID, v.PriceVersion, v.ProductID, requestID)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ListOptionsForVariants batch-looks-up every option selection for several
// variants at once, keyed by variant id — for building a display response
// without one query per variant.
func (r *ProductVariantRepository) ListOptionsForVariants(ctx context.Context, variantIDs []string) (map[string][]domain.VariantOptionSelection, error) {
	if len(variantIDs) == 0 {
		return map[string][]domain.VariantOptionSelection{}, nil
	}
	const query = `SELECT variant_id, attribute_id, option_id FROM product_variant_options WHERE variant_id = ANY($1)`
	rows, err := connection(ctx, r.pool).Query(ctx, query, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]domain.VariantOptionSelection, len(variantIDs))
	for rows.Next() {
		var variantID string
		var s domain.VariantOptionSelection
		if err := rows.Scan(&variantID, &s.AttributeID, &s.OptionID); err != nil {
			return nil, err
		}
		out[variantID] = append(out[variantID], s)
	}
	return out, rows.Err()
}
