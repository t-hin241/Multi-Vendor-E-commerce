package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
func (r *ProductVariantRepository) Create(ctx context.Context, v *domain.ProductVariant, selections []domain.VariantOptionSelection) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const insertVariant = `
		INSERT INTO product_variants (product_id, sku, variant_key)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`
	if err := tx.QueryRow(ctx, insertVariant, v.ProductID, v.SKU, v.VariantKey).Scan(&v.ID, &v.CreatedAt); err != nil {
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

	return tx.Commit(ctx)
}

func (r *ProductVariantRepository) FindByID(ctx context.Context, id string) (*domain.ProductVariant, error) {
	const query = `SELECT id, product_id, sku, variant_key, created_at FROM product_variants WHERE id = $1`
	var v domain.ProductVariant
	err := r.pool.QueryRow(ctx, query, id).Scan(&v.ID, &v.ProductID, &v.SKU, &v.VariantKey, &v.CreatedAt)
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
	err := r.pool.QueryRow(ctx, query, productID).Scan(&exists)
	return exists, err
}

func (r *ProductVariantRepository) ListForProduct(ctx context.Context, productID string) ([]*domain.ProductVariant, error) {
	const query = `SELECT id, product_id, sku, variant_key, created_at FROM product_variants WHERE product_id = $1 ORDER BY created_at ASC`
	rows, err := r.pool.Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []*domain.ProductVariant
	for rows.Next() {
		var v domain.ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.VariantKey, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, &v)
	}
	return variants, rows.Err()
}

// ListOptionsForVariants batch-looks-up every option selection for several
// variants at once, keyed by variant id — for building a display response
// without one query per variant.
func (r *ProductVariantRepository) ListOptionsForVariants(ctx context.Context, variantIDs []string) (map[string][]domain.VariantOptionSelection, error) {
	if len(variantIDs) == 0 {
		return map[string][]domain.VariantOptionSelection{}, nil
	}
	const query = `SELECT variant_id, attribute_id, option_id FROM product_variant_options WHERE variant_id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, variantIDs)
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
