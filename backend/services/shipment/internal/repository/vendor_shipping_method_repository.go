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
	ErrVendorShippingMethodNotFound      = errors.New("repository: vendor shipping method not found")
	ErrVendorShippingMethodAlreadyExists = errors.New("repository: carrier already enabled for this vendor")
)

const vendorShippingMethodColumns = `id, vendor_id, carrier_id, is_default, is_active, created_at`

type VendorShippingMethodRepository struct {
	pool *pgxpool.Pool
}

func NewVendorShippingMethodRepository(pool *pgxpool.Pool) *VendorShippingMethodRepository {
	return &VendorShippingMethodRepository{pool: pool}
}

func scanVendorShippingMethod(row pgx.Row) (*domain.VendorShippingMethod, error) {
	var m domain.VendorShippingMethod
	err := row.Scan(&m.ID, &m.VendorID, &m.CarrierID, &m.IsDefault, &m.IsActive, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVendorShippingMethodNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r *VendorShippingMethodRepository) Create(ctx context.Context, m *domain.VendorShippingMethod) error {
	const query = `
		INSERT INTO vendor_shipping_methods (vendor_id, carrier_id, is_default, is_active)
		VALUES ($1, $2, $3, TRUE)
		RETURNING id, is_active, created_at`
	err := r.pool.QueryRow(ctx, query, m.VendorID, m.CarrierID, m.IsDefault).Scan(&m.ID, &m.IsActive, &m.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrVendorShippingMethodAlreadyExists
		}
		return err
	}
	return nil
}

func (r *VendorShippingMethodRepository) ListForVendor(ctx context.Context, vendorID string) ([]*domain.VendorShippingMethod, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+vendorShippingMethodColumns+` FROM vendor_shipping_methods WHERE vendor_id = $1 ORDER BY created_at ASC`, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.VendorShippingMethod
	for rows.Next() {
		m, err := scanVendorShippingMethod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *VendorShippingMethodRepository) FindByID(ctx context.Context, id string) (*domain.VendorShippingMethod, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+vendorShippingMethodColumns+` FROM vendor_shipping_methods WHERE id = $1`, id)
	return scanVendorShippingMethod(row)
}

// FindDefaultForVendor is the lookup the automatic shipment-creation path
// depends on: the one carrier the system uses with zero buyer input.
func (r *VendorShippingMethodRepository) FindDefaultForVendor(ctx context.Context, vendorID string) (*domain.VendorShippingMethod, error) {
	const query = `SELECT ` + vendorShippingMethodColumns + ` FROM vendor_shipping_methods WHERE vendor_id = $1 AND is_default AND is_active`
	row := r.pool.QueryRow(ctx, query, vendorID)
	return scanVendorShippingMethod(row)
}

// SetDefault clears any existing default for the vendor and marks the
// given method as the new one, inside a transaction so there's never a
// moment with zero or two defaults visible to a concurrent reader.
func (r *VendorShippingMethodRepository) SetDefault(ctx context.Context, vendorID, methodID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE vendor_shipping_methods SET is_default = FALSE WHERE vendor_id = $1`, vendorID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE vendor_shipping_methods SET is_default = TRUE WHERE id = $1 AND vendor_id = $2`, methodID, vendorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorShippingMethodNotFound
	}
	return tx.Commit(ctx)
}

func (r *VendorShippingMethodRepository) SetActive(ctx context.Context, vendorID, methodID string, isActive bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE vendor_shipping_methods SET is_active = $1 WHERE id = $2 AND vendor_id = $3`, isActive, methodID, vendorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorShippingMethodNotFound
	}
	return nil
}
