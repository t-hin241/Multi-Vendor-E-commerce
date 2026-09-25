package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type ProductImageRepository struct {
	pool *pgxpool.Pool
}

func NewProductImageRepository(pool *pgxpool.Pool) *ProductImageRepository {
	return &ProductImageRepository{pool: pool}
}

// ReplaceForProduct atomically swaps a product's single main image: it
// deletes every existing row for the product and inserts img in one
// transaction, so the product never has zero or two images at once. It
// returns the object keys of whatever was deleted, so the caller can clean
// up the now-orphaned files in object storage.
func (r *ProductImageRepository) ReplaceForProduct(ctx context.Context, img *domain.ProductImage) ([]string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `DELETE FROM product_images WHERE product_id = $1 RETURNING object_key`, img.ProductID)
	if err != nil {
		return nil, err
	}
	var oldKeys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		oldKeys = append(oldKeys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const insert = `
		INSERT INTO product_images (product_id, object_key, url, position)
		VALUES ($1, $2, $3, 0)
		RETURNING id, created_at`
	if err := tx.QueryRow(ctx, insert, img.ProductID, img.ObjectKey, img.URL).Scan(&img.ID, &img.CreatedAt); err != nil {
		return nil, err
	}
	img.Position = 0

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return oldKeys, nil
}

// DeleteForProduct removes a product's main image entirely, with no
// replacement — the delete half of ReplaceForProduct, on its own.
func (r *ProductImageRepository) DeleteForProduct(ctx context.Context, productID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `DELETE FROM product_images WHERE product_id = $1 RETURNING object_key`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deletedKeys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		deletedKeys = append(deletedKeys, key)
	}
	return deletedKeys, rows.Err()
}

// ListForProducts batch-looks-up the main image for many products at once
// (at most one per product, per the single-main-image invariant), for the
// storefront listing so it doesn't issue one query per product.
func (r *ProductImageRepository) ListForProducts(ctx context.Context, productIDs []string) (map[string]*domain.ProductImage, error) {
	if len(productIDs) == 0 {
		return map[string]*domain.ProductImage{}, nil
	}
	const query = `SELECT id, product_id, object_key, url, position, created_at FROM product_images WHERE product_id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*domain.ProductImage, len(productIDs))
	for rows.Next() {
		var img domain.ProductImage
		if err := rows.Scan(&img.ID, &img.ProductID, &img.ObjectKey, &img.URL, &img.Position, &img.CreatedAt); err != nil {
			return nil, err
		}
		out[img.ProductID] = &img
	}
	return out, rows.Err()
}

func (r *ProductImageRepository) ListForProduct(ctx context.Context, productID string) ([]*domain.ProductImage, error) {
	const query = `SELECT id, product_id, object_key, url, position, created_at FROM product_images WHERE product_id = $1 ORDER BY position ASC`
	rows, err := r.pool.Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*domain.ProductImage
	for rows.Next() {
		var img domain.ProductImage
		if err := rows.Scan(&img.ID, &img.ProductID, &img.ObjectKey, &img.URL, &img.Position, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, &img)
	}
	return images, rows.Err()
}
