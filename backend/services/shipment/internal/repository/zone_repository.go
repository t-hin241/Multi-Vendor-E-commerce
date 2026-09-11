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
	ErrZoneNotFound          = errors.New("repository: zone not found")
	ErrZoneAlreadyExists     = errors.New("repository: zone code already exists")
	ErrProvinceAlreadyMapped = errors.New("repository: province is already mapped to a zone")
)

const zoneColumns = `id, name, code, created_at`

type ZoneRepository struct {
	pool *pgxpool.Pool
}

func NewZoneRepository(pool *pgxpool.Pool) *ZoneRepository {
	return &ZoneRepository{pool: pool}
}

func scanZone(row pgx.Row) (*domain.Zone, error) {
	var z domain.Zone
	if err := row.Scan(&z.ID, &z.Name, &z.Code, &z.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrZoneNotFound
		}
		return nil, err
	}
	return &z, nil
}

func (r *ZoneRepository) Create(ctx context.Context, z *domain.Zone) error {
	const query = `INSERT INTO shipping_zones (name, code) VALUES ($1, $2) RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, query, z.Name, z.Code).Scan(&z.ID, &z.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrZoneAlreadyExists
		}
		return err
	}
	return nil
}

func (r *ZoneRepository) FindByID(ctx context.Context, id string) (*domain.Zone, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+zoneColumns+` FROM shipping_zones WHERE id = $1`, id)
	return scanZone(row)
}

func (r *ZoneRepository) List(ctx context.Context) ([]*domain.Zone, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+zoneColumns+` FROM shipping_zones ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Zone
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// AddProvince maps one province code to a zone. A province can belong to
// at most one zone at a time (enforced by a unique index on province_code
// alone), so re-mapping an already-mapped province fails rather than
// silently moving it.
func (r *ZoneRepository) AddProvince(ctx context.Context, zoneID, provinceCode string) error {
	const query = `INSERT INTO shipping_zone_provinces (zone_id, province_code) VALUES ($1, $2)`
	_, err := r.pool.Exec(ctx, query, zoneID, provinceCode)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrProvinceAlreadyMapped
		}
		return err
	}
	return nil
}

func (r *ZoneRepository) ListProvinces(ctx context.Context, zoneID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT province_code FROM shipping_zone_provinces WHERE zone_id = $1 ORDER BY province_code ASC`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// FindZoneByProvinceCode resolves a buyer's destination province to the
// zone it's mapped to — the sole lookup the fee-quoting path needs.
func (r *ZoneRepository) FindZoneByProvinceCode(ctx context.Context, provinceCode string) (*domain.Zone, error) {
	const query = `
		SELECT z.id, z.name, z.code, z.created_at
		FROM shipping_zones z
		JOIN shipping_zone_provinces p ON p.zone_id = z.id
		WHERE p.province_code = $1`
	row := r.pool.QueryRow(ctx, query, provinceCode)
	return scanZone(row)
}
