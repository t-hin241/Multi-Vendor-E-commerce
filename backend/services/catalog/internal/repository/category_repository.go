package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type CategoryRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryRepository(pool *pgxpool.Pool) *CategoryRepository {
	return &CategoryRepository{pool: pool}
}

var ErrCategoryNotFound = errors.New("repository: category not found")
var ErrSlugTaken = errors.New("repository: slug already taken")

func (r *CategoryRepository) Create(ctx context.Context, c *domain.Category) error {
	const query = `INSERT INTO categories (name, slug, parent_id, level) VALUES ($1, $2, $3, $4) RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, query, c.Name, c.Slug, c.ParentID, c.Level).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrSlugTaken
		}
		return err
	}
	return nil
}

func (r *CategoryRepository) FindByID(ctx context.Context, id string) (*domain.Category, error) {
	const query = `SELECT id, name, slug, parent_id, level, created_at FROM categories WHERE id = $1`
	var c domain.Category
	err := r.pool.QueryRow(ctx, query, id).Scan(&c.ID, &c.Name, &c.Slug, &c.ParentID, &c.Level, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCategoryNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CategoryRepository) SlugExists(ctx context.Context, slug string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM categories WHERE slug = $1)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, slug).Scan(&exists)
	return exists, err
}

func (r *CategoryRepository) List(ctx context.Context) ([]*domain.Category, error) {
	const query = `SELECT id, name, slug, parent_id, level, created_at FROM categories ORDER BY name ASC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []*domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ParentID, &c.Level, &c.CreatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, &c)
	}
	return categories, rows.Err()
}
