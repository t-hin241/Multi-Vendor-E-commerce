package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

var ErrBuyerAddressNotFound = errors.New("repository: buyer address not found")

const buyerAddressColumns = `id, buyer_id, recipient_name, phone, province, district, ward, street_address, is_default, created_at, updated_at`

type BuyerAddressRepository struct {
	pool *pgxpool.Pool
}

func NewBuyerAddressRepository(pool *pgxpool.Pool) *BuyerAddressRepository {
	return &BuyerAddressRepository{pool: pool}
}

func scanBuyerAddress(row pgx.Row) (*domain.BuyerAddress, error) {
	var a domain.BuyerAddress
	err := row.Scan(&a.ID, &a.BuyerID, &a.RecipientName, &a.Phone, &a.Province, &a.District, &a.Ward, &a.StreetAddress, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBuyerAddressNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *BuyerAddressRepository) Create(ctx context.Context, a *domain.BuyerAddress) error {
	const query = `
		INSERT INTO buyer_addresses (buyer_id, recipient_name, phone, province, district, ward, street_address, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`
	return r.pool.QueryRow(ctx, query, a.BuyerID, a.RecipientName, a.Phone, a.Province, a.District, a.Ward, a.StreetAddress, a.IsDefault).
		Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (r *BuyerAddressRepository) FindByID(ctx context.Context, id string) (*domain.BuyerAddress, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+buyerAddressColumns+` FROM buyer_addresses WHERE id = $1`, id)
	return scanBuyerAddress(row)
}

func (r *BuyerAddressRepository) ListForBuyer(ctx context.Context, buyerID string) ([]*domain.BuyerAddress, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+buyerAddressColumns+` FROM buyer_addresses WHERE buyer_id = $1 ORDER BY created_at ASC`, buyerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.BuyerAddress
	for rows.Next() {
		a, err := scanBuyerAddress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *BuyerAddressRepository) FindDefaultForBuyer(ctx context.Context, buyerID string) (*domain.BuyerAddress, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+buyerAddressColumns+` FROM buyer_addresses WHERE buyer_id = $1 AND is_default`, buyerID)
	return scanBuyerAddress(row)
}

func (r *BuyerAddressRepository) Update(ctx context.Context, id string, a *domain.BuyerAddress) error {
	const query = `
		UPDATE buyer_addresses
		SET recipient_name = $1, phone = $2, province = $3, district = $4, ward = $5, street_address = $6, updated_at = now()
		WHERE id = $7`
	tag, err := r.pool.Exec(ctx, query, a.RecipientName, a.Phone, a.Province, a.District, a.Ward, a.StreetAddress, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBuyerAddressNotFound
	}
	return nil
}

func (r *BuyerAddressRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM buyer_addresses WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBuyerAddressNotFound
	}
	return nil
}

// SetDefault clears any existing default for the buyer and marks the given
// address as the new one inside a transaction, so there's never a moment
// with zero or two defaults visible to a concurrent reader.
func (r *BuyerAddressRepository) SetDefault(ctx context.Context, buyerID, addressID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE buyer_addresses SET is_default = FALSE WHERE buyer_id = $1`, buyerID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE buyer_addresses SET is_default = TRUE WHERE id = $1 AND buyer_id = $2`, addressID, buyerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBuyerAddressNotFound
	}
	return tx.Commit(ctx)
}
