package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type ProductAttributeValueRepository struct {
	pool *pgxpool.Pool
}

func NewProductAttributeValueRepository(pool *pgxpool.Pool) *ProductAttributeValueRepository {
	return &ProductAttributeValueRepository{pool: pool}
}

// ReplaceForProduct atomically swaps a product's whole attribute-value set:
// deletes every existing row for the product and inserts the new set in one
// transaction, mirroring ProductImageRepository.ReplaceForProduct.
func (r *ProductAttributeValueRepository) ReplaceForProduct(ctx context.Context, productID string, values []*domain.ProductAttributeValue) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM product_attribute_values WHERE product_id = $1`, productID); err != nil {
		return err
	}

	const insert = `
		INSERT INTO product_attribute_values (product_id, attribute_id, rule_id, option_id, value_text, value_number, value_boolean)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	for _, v := range values {
		if err := tx.QueryRow(ctx, insert,
			productID, v.AttributeID, v.RuleID, v.OptionID, v.ValueText, v.ValueNumber, v.ValueBoolean,
		).Scan(&v.ID, &v.CreatedAt); err != nil {
			return err
		}
		v.ProductID = productID
	}

	return tx.Commit(ctx)
}

func (r *ProductAttributeValueRepository) ListForProduct(ctx context.Context, productID string) ([]*domain.ProductAttributeValue, error) {
	const query = `
		SELECT id, product_id, attribute_id, rule_id, option_id, value_text, value_number, value_boolean, created_at
		FROM product_attribute_values WHERE product_id = $1`
	rows, err := r.pool.Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.ProductAttributeValue
	for rows.Next() {
		var v domain.ProductAttributeValue
		if err := rows.Scan(&v.ID, &v.ProductID, &v.AttributeID, &v.RuleID, &v.OptionID, &v.ValueText, &v.ValueNumber, &v.ValueBoolean, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}
