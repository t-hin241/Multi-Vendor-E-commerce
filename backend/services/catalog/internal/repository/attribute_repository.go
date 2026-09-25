package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/catalog/internal/domain"
)

type AttributeRepository struct {
	pool *pgxpool.Pool
}

func NewAttributeRepository(pool *pgxpool.Pool) *AttributeRepository {
	return &AttributeRepository{pool: pool}
}

var ErrAttributeNotFound = errors.New("repository: attribute not found")
var ErrAttributeCodeTaken = errors.New("repository: attribute code already taken")
var ErrOptionValueTaken = errors.New("repository: option value already taken for this attribute")

func (r *AttributeRepository) Create(ctx context.Context, a *domain.Attribute) error {
	const query = `
		INSERT INTO attributes (code, name, data_type, unit, is_variant_defining)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, is_active, created_at`
	err := r.pool.QueryRow(ctx, query, a.Code, a.Name, a.DataType, a.Unit, a.IsVariantDefining).Scan(&a.ID, &a.IsActive, &a.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAttributeCodeTaken
		}
		return err
	}
	return nil
}

func (r *AttributeRepository) FindByID(ctx context.Context, id string) (*domain.Attribute, error) {
	const query = `SELECT id, code, name, data_type, unit, is_active, is_variant_defining, created_at FROM attributes WHERE id = $1`
	var a domain.Attribute
	err := r.pool.QueryRow(ctx, query, id).Scan(&a.ID, &a.Code, &a.Name, &a.DataType, &a.Unit, &a.IsActive, &a.IsVariantDefining, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttributeNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *AttributeRepository) List(ctx context.Context) ([]*domain.Attribute, error) {
	const query = `SELECT id, code, name, data_type, unit, is_active, is_variant_defining, created_at FROM attributes ORDER BY name ASC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attributes []*domain.Attribute
	for rows.Next() {
		var a domain.Attribute
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.DataType, &a.Unit, &a.IsActive, &a.IsVariantDefining, &a.CreatedAt); err != nil {
			return nil, err
		}
		attributes = append(attributes, &a)
	}
	return attributes, rows.Err()
}

// ListByIDs batch-looks-up attributes for template building, so resolving a
// category's effective attribute set doesn't issue one query per attribute.
func (r *AttributeRepository) ListByIDs(ctx context.Context, ids []string) (map[string]*domain.Attribute, error) {
	if len(ids) == 0 {
		return map[string]*domain.Attribute{}, nil
	}
	const query = `SELECT id, code, name, data_type, unit, is_active, is_variant_defining, created_at FROM attributes WHERE id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*domain.Attribute, len(ids))
	for rows.Next() {
		var a domain.Attribute
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.DataType, &a.Unit, &a.IsActive, &a.IsVariantDefining, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[a.ID] = &a
	}
	return out, rows.Err()
}

func (r *AttributeRepository) CountOptionsForAttribute(ctx context.Context, attributeID string) (int, error) {
	const query = `SELECT count(*) FROM attribute_options WHERE attribute_id = $1`
	var count int
	err := r.pool.QueryRow(ctx, query, attributeID).Scan(&count)
	return count, err
}

func (r *AttributeRepository) AddOption(ctx context.Context, o *domain.AttributeOption) error {
	const query = `
		INSERT INTO attribute_options (attribute_id, value, position)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, query, o.AttributeID, o.Value, o.Position).Scan(&o.ID, &o.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrOptionValueTaken
		}
		return err
	}
	return nil
}

// ListOptionsForAttributes batch-looks-up every option for several
// attributes at once, keyed by attribute id — for template building.
func (r *AttributeRepository) ListOptionsForAttributes(ctx context.Context, attributeIDs []string) (map[string][]*domain.AttributeOption, error) {
	if len(attributeIDs) == 0 {
		return map[string][]*domain.AttributeOption{}, nil
	}
	const query = `
		SELECT id, attribute_id, value, position, created_at
		FROM attribute_options WHERE attribute_id = ANY($1)
		ORDER BY attribute_id, position ASC`
	rows, err := r.pool.Query(ctx, query, attributeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]*domain.AttributeOption, len(attributeIDs))
	for rows.Next() {
		var o domain.AttributeOption
		if err := rows.Scan(&o.ID, &o.AttributeID, &o.Value, &o.Position, &o.CreatedAt); err != nil {
			return nil, err
		}
		out[o.AttributeID] = append(out[o.AttributeID], &o)
	}
	return out, rows.Err()
}

// ListOptionsByIDs batch-looks-up specific options by id, keyed by option
// id — used to validate submitted option ids belong to the attribute they
// were submitted for.
func (r *AttributeRepository) ListOptionsByIDs(ctx context.Context, ids []string) (map[string]*domain.AttributeOption, error) {
	if len(ids) == 0 {
		return map[string]*domain.AttributeOption{}, nil
	}
	const query = `SELECT id, attribute_id, value, position, created_at FROM attribute_options WHERE id = ANY($1)`
	rows, err := r.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*domain.AttributeOption, len(ids))
	for rows.Next() {
		var o domain.AttributeOption
		if err := rows.Scan(&o.ID, &o.AttributeID, &o.Value, &o.Position, &o.CreatedAt); err != nil {
			return nil, err
		}
		out[o.ID] = &o
	}
	return out, rows.Err()
}
