package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type ProductPackagingRepository struct {
	pool *pgxpool.Pool
}

func NewProductPackagingRepository(pool *pgxpool.Pool) *ProductPackagingRepository {
	return &ProductPackagingRepository{pool: pool}
}

// Upsert replaces a product's packaging record — same "replace, don't
// merge" convention as ProductAttributeValueRepository.ReplaceForProduct,
// since a product update always resubmits its full packaging set.
func (r *ProductPackagingRepository) Upsert(ctx context.Context, productID string, p domain.Packaging) error {
	const query = `
		INSERT INTO product_packaging (product_id, weight_grams, length_mm, width_mm, height_mm, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (product_id) DO UPDATE SET
			weight_grams = EXCLUDED.weight_grams,
			length_mm = EXCLUDED.length_mm,
			width_mm = EXCLUDED.width_mm,
			height_mm = EXCLUDED.height_mm,
			updated_at = now()`
	_, err := r.pool.Exec(ctx, query, productID, p.WeightGrams, p.LengthMM, p.WidthMM, p.HeightMM)
	return err
}

func (r *ProductPackagingRepository) Get(ctx context.Context, productID string) (domain.Packaging, error) {
	const query = `SELECT weight_grams, length_mm, width_mm, height_mm FROM product_packaging WHERE product_id = $1`
	var p domain.Packaging
	err := r.pool.QueryRow(ctx, query, productID).Scan(&p.WeightGrams, &p.LengthMM, &p.WidthMM, &p.HeightMM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Packaging{}, nil
		}
		return domain.Packaging{}, err
	}
	return p, nil
}

// ListByProductIDs batch-resolves packaging for many products at once, for
// Order's per-checkout-line weight lookup.
func (r *ProductPackagingRepository) ListByProductIDs(ctx context.Context, productIDs []string) (map[string]domain.Packaging, error) {
	const query = `SELECT product_id, weight_grams, length_mm, width_mm, height_mm FROM product_packaging WHERE product_id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]domain.Packaging, len(productIDs))
	for rows.Next() {
		var productID string
		var p domain.Packaging
		if err := rows.Scan(&productID, &p.WeightGrams, &p.LengthMM, &p.WidthMM, &p.HeightMM); err != nil {
			return nil, err
		}
		out[productID] = p
	}
	return out, rows.Err()
}
