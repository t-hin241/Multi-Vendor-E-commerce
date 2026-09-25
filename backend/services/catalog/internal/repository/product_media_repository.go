package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type ProductMediaRepository struct {
	pool *pgxpool.Pool
}

func NewProductMediaRepository(pool *pgxpool.Pool) *ProductMediaRepository {
	return &ProductMediaRepository{pool: pool}
}

func (r *ProductMediaRepository) Create(ctx context.Context, m *domain.ProductMedia) error {
	const query = `
		INSERT INTO product_media (product_id, kind, object_key, url, content_type, size_bytes, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	return r.pool.QueryRow(ctx, query, m.ProductID, m.Kind, m.ObjectKey, m.URL, m.ContentType, m.SizeBytes, m.Position).
		Scan(&m.ID, &m.CreatedAt)
}

func (r *ProductMediaRepository) CountForProduct(ctx context.Context, productID string) (int, error) {
	const query = `SELECT count(*) FROM product_media WHERE product_id = $1`
	var count int
	err := r.pool.QueryRow(ctx, query, productID).Scan(&count)
	return count, err
}

func (r *ProductMediaRepository) ListForProduct(ctx context.Context, productID string) ([]*domain.ProductMedia, error) {
	const query = `
		SELECT id, product_id, kind, object_key, url, content_type, size_bytes, position, created_at
		FROM product_media WHERE product_id = $1 ORDER BY position ASC`
	rows, err := r.pool.Query(ctx, query, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*domain.ProductMedia
	for rows.Next() {
		var m domain.ProductMedia
		if err := rows.Scan(&m.ID, &m.ProductID, &m.Kind, &m.ObjectKey, &m.URL, &m.ContentType, &m.SizeBytes, &m.Position, &m.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &m)
	}
	return items, rows.Err()
}
