package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

var ErrVendorAddressNotFound = errors.New("repository: vendor address not found")

const vendorAddressColumns = `id, vendor_id, recipient_name, phone, province, district, ward, street_address, is_default, created_at, updated_at`

type VendorAddressRepository struct {
	pool *pgxpool.Pool
}

func NewVendorAddressRepository(pool *pgxpool.Pool) *VendorAddressRepository {
	return &VendorAddressRepository{pool: pool}
}

func scanVendorAddress(row pgx.Row) (*domain.VendorAddress, error) {
	var a domain.VendorAddress
	err := row.Scan(&a.ID, &a.VendorID, &a.RecipientName, &a.Phone, &a.Province, &a.District, &a.Ward, &a.StreetAddress, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVendorAddressNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *VendorAddressRepository) Create(ctx context.Context, a *domain.VendorAddress) error {
	const query = `
		INSERT INTO vendor_addresses (vendor_id, recipient_name, phone, province, district, ward, street_address, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`
	return r.pool.QueryRow(ctx, query, a.VendorID, a.RecipientName, a.Phone, a.Province, a.District, a.Ward, a.StreetAddress, a.IsDefault).
		Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (r *VendorAddressRepository) FindByID(ctx context.Context, id string) (*domain.VendorAddress, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+vendorAddressColumns+` FROM vendor_addresses WHERE id = $1`, id)
	return scanVendorAddress(row)
}

func (r *VendorAddressRepository) ListForVendor(ctx context.Context, vendorID string) ([]*domain.VendorAddress, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+vendorAddressColumns+` FROM vendor_addresses WHERE vendor_id = $1 ORDER BY created_at ASC`, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.VendorAddress
	for rows.Next() {
		a, err := scanVendorAddress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *VendorAddressRepository) FindDefaultForVendor(ctx context.Context, vendorID string) (*domain.VendorAddress, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+vendorAddressColumns+` FROM vendor_addresses WHERE vendor_id = $1 AND is_default`, vendorID)
	return scanVendorAddress(row)
}

func (r *VendorAddressRepository) Update(ctx context.Context, id string, a *domain.VendorAddress) error {
	const query = `
		UPDATE vendor_addresses
		SET recipient_name = $1, phone = $2, province = $3, district = $4, ward = $5, street_address = $6, updated_at = now()
		WHERE id = $7`
	tag, err := r.pool.Exec(ctx, query, a.RecipientName, a.Phone, a.Province, a.District, a.Ward, a.StreetAddress, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorAddressNotFound
	}
	return nil
}

func (r *VendorAddressRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM vendor_addresses WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorAddressNotFound
	}
	return nil
}

// SetDefault clears any existing default for the vendor and marks the
// given address as the new one inside a transaction, so there's never a
// moment with zero or two defaults visible to a concurrent reader.
func (r *VendorAddressRepository) SetDefault(ctx context.Context, vendorID, addressID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE vendor_addresses SET is_default = FALSE WHERE vendor_id = $1`, vendorID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE vendor_addresses SET is_default = TRUE WHERE id = $1 AND vendor_id = $2`, addressID, vendorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorAddressNotFound
	}
	return tx.Commit(ctx)
}
