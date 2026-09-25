package repository

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type ProductRepository struct {
	pool *pgxpool.Pool
}

func NewProductRepository(pool *pgxpool.Pool) *ProductRepository {
	return &ProductRepository{pool: pool}
}

var ErrProductNotFound = errors.New("repository: product not found")

const productSelectColumns = `
	SELECT id, vendor_id, category_id, name, slug, description, price_amount, currency,
	       status, rejection_reason, is_active, created_at, updated_at
	`

func (r *ProductRepository) Create(ctx context.Context, p *domain.Product) error {
	const query = `
		INSERT INTO products (vendor_id, category_id, name, slug, description, price_amount, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, status, is_active, created_at, updated_at`

	err := r.pool.QueryRow(ctx, query, p.VendorID, p.CategoryID, p.Name, p.Slug, p.Description, p.PriceAmount, p.Currency).
		Scan(&p.ID, &p.Status, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrSlugTaken
		}
		return err
	}
	return nil
}

func (r *ProductRepository) SlugExists(ctx context.Context, slug string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM products WHERE slug = $1)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, slug).Scan(&exists)
	return exists, err
}

func (r *ProductRepository) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	return scanProduct(r.pool.QueryRow(ctx, productSelectColumns+`FROM products WHERE id = $1`, id))
}

func (r *ProductRepository) FindBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return scanProduct(r.pool.QueryRow(ctx, productSelectColumns+`FROM products WHERE slug = $1`, slug))
}

func (r *ProductRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Product, error) {
	rows, err := r.pool.Query(ctx,
		productSelectColumns+`FROM products WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		vendorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProducts(rows)
}

func (r *ProductRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Product, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if status == "" {
		rows, err = r.pool.Query(ctx, productSelectColumns+`FROM products ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = r.pool.Query(ctx, productSelectColumns+`FROM products WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProducts(rows)
}

// storefrontWhereClause builds the WHERE clause shared by ListStorefront and
// CountStorefront, so the two never drift apart on what counts as "visible".
func storefrontWhereClause(categoryID, vendorID, search string) (clause string, args []any) {
	clause = `WHERE status = 'approved' AND is_active = TRUE`

	if vendorID != "" {
		args = append(args, vendorID)
		clause += ` AND vendor_id = $` + strconv.Itoa(len(args))
	}
	if categoryID != "" {
		args = append(args, categoryID)
		// Products can be attached at any depth under the selected category
		// (categories only carry their own leaf-ish products, not those of
		// their descendants), so browsing a parent node must include every
		// descendant's products too, not just an exact category_id match.
		clause += ` AND category_id IN (
			WITH RECURSIVE descendants AS (
				SELECT id FROM categories WHERE id = $` + strconv.Itoa(len(args)) + `
				UNION ALL
				SELECT c.id FROM categories c JOIN descendants d ON c.parent_id = d.id
			)
			SELECT id FROM descendants
		)`
	}
	if search != "" {
		args = append(args, search)
		clause += ` AND name ILIKE '%' || $` + strconv.Itoa(len(args)) + ` || '%'`
	}
	return clause, args
}

// ListStorefront returns only publicly visible products (approved and
// active), optionally filtered by category, vendor and a case-insensitive
// name search.
func (r *ProductRepository) ListStorefront(ctx context.Context, categoryID, vendorID, search string, limit, offset int) ([]*domain.Product, error) {
	where, args := storefrontWhereClause(categoryID, vendorID, search)
	query := productSelectColumns + `FROM products ` + where

	args = append(args, limit, offset)
	query += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProducts(rows)
}

// CountStorefront returns the total number of products ListStorefront would
// return across all pages for the same filters, so the client can render
// page-number pagination instead of an open-ended "load more."
func (r *ProductRepository) CountStorefront(ctx context.Context, categoryID, vendorID, search string) (int, error) {
	where, args := storefrontWhereClause(categoryID, vendorID, search)
	query := `SELECT count(*) FROM products ` + where

	var total int
	err := r.pool.QueryRow(ctx, query, args...).Scan(&total)
	return total, err
}

func (r *ProductRepository) UpdateStatus(ctx context.Context, id string, status domain.Status, rejectionReason *string) error {
	const query = `UPDATE products SET status = $1, rejection_reason = $2, updated_at = now() WHERE id = $3`
	tag, err := r.pool.Exec(ctx, query, status, rejectionReason, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotFound
	}
	return nil
}

func (r *ProductRepository) UpdateActive(ctx context.Context, id string, isActive bool) error {
	const query = `UPDATE products SET is_active = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, isActive, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotFound
	}
	return nil
}

func scanProduct(row pgx.Row) (*domain.Product, error) {
	var p domain.Product
	err := row.Scan(&p.ID, &p.VendorID, &p.CategoryID, &p.Name, &p.Slug, &p.Description, &p.PriceAmount, &p.Currency,
		&p.Status, &p.RejectionReason, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, err
	}
	return &p, nil
}

func scanProducts(rows pgx.Rows) ([]*domain.Product, error) {
	var products []*domain.Product
	for rows.Next() {
		var p domain.Product
		err := rows.Scan(&p.ID, &p.VendorID, &p.CategoryID, &p.Name, &p.Slug, &p.Description, &p.PriceAmount, &p.Currency,
			&p.Status, &p.RejectionReason, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		products = append(products, &p)
	}
	return products, rows.Err()
}
