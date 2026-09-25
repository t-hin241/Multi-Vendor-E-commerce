package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/shipment/internal/domain"
)

var (
	ErrCarrierNotFound      = errors.New("repository: carrier not found")
	ErrCarrierAlreadyExists = errors.New("repository: carrier code already exists")
)

const carrierColumns = `id, name, code, is_active, created_at, updated_at`

type CarrierRepository struct {
	pool *pgxpool.Pool
}

func NewCarrierRepository(pool *pgxpool.Pool) *CarrierRepository {
	return &CarrierRepository{pool: pool}
}

func scanCarrier(row pgx.Row) (*domain.Carrier, error) {
	var c domain.Carrier
	if err := row.Scan(&c.ID, &c.Name, &c.Code, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCarrierNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *CarrierRepository) Create(ctx context.Context, c *domain.Carrier) error {
	const query = `INSERT INTO carriers (name, code) VALUES ($1, $2) RETURNING id, is_active, created_at, updated_at`
	err := r.pool.QueryRow(ctx, query, c.Name, c.Code).Scan(&c.ID, &c.IsActive, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrCarrierAlreadyExists
		}
		return err
	}
	return nil
}

func (r *CarrierRepository) FindByID(ctx context.Context, id string) (*domain.Carrier, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+carrierColumns+` FROM carriers WHERE id = $1`, id)
	return scanCarrier(row)
}

func (r *CarrierRepository) List(ctx context.Context) ([]*domain.Carrier, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+carrierColumns+` FROM carriers ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Carrier
	for rows.Next() {
		c, err := scanCarrier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CarrierRepository) SetActive(ctx context.Context, id string, isActive bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE carriers SET is_active = $1, updated_at = now() WHERE id = $2`, isActive, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCarrierNotFound
	}
	return nil
}
