package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

var ErrReturnDestinationNotFound = errors.New("repository: return destination not found")

// ReturnDestinationRepository stores AF-05 return destinations.
type ReturnDestinationRepository struct{ Pool *pgxpool.Pool }

const returnDestinationColumns = `d.vendor_id, d.address_id, d.receiving_hours, d.version, d.verified_version, d.verified_by, d.verified_at,
	d.rejection_reason, d.updated_by, d.created_at, d.updated_at,
	a.id, a.vendor_id, a.recipient_name, a.phone, a.province, a.district, a.ward, a.street_address, a.is_default, a.created_at, a.updated_at`

// Find is the shop's destination with its address; inside a transaction
// the row is locked.
func (r ReturnDestinationRepository) Find(ctx context.Context, vendorID string) (*domain.ReturnDestination, error) {
	lock := ""
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		lock = " FOR UPDATE OF d"
	}
	var d domain.ReturnDestination
	var a domain.VendorAddress
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+returnDestinationColumns+` FROM vendor_return_destinations d
		JOIN vendor_addresses a ON a.id = d.address_id WHERE d.vendor_id = $1`+lock, vendorID).
		Scan(&d.VendorID, &d.AddressID, &d.ReceivingHours, &d.Version, &d.VerifiedVersion, &d.VerifiedBy, &d.VerifiedAt, &d.RejectionReason,
			&d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt, &a.ID, &a.VendorID, &a.RecipientName, &a.Phone, &a.Province, &a.District, &a.Ward,
			&a.StreetAddress, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReturnDestinationNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Address = &a
	return &d, nil
}

// Set designates an address with its hours; the version goes up and the
// verification no longer matches.
func (r ReturnDestinationRepository) Set(ctx context.Context, vendorID, addressID, hours, actor string) (int64, error) {
	var version int64
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO vendor_return_destinations (vendor_id, address_id, receiving_hours, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (vendor_id) DO UPDATE SET address_id = EXCLUDED.address_id, receiving_hours = EXCLUDED.receiving_hours,
			updated_by = EXCLUDED.updated_by, version = vendor_return_destinations.version + 1, rejection_reason = NULL, updated_at = now()
		RETURNING version`, vendorID, addressID, hours, actor).Scan(&version)
	return version, err
}

// Decide records an admin's verification or rejection of one version.
func (r ReturnDestinationRepository) Decide(ctx context.Context, vendorID string, version int64, actor string, verify bool, reason *string) error {
	var tag interface{ RowsAffected() int64 }
	var err error
	if verify {
		tag, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_return_destinations SET verified_version = version, verified_by = $3,
			verified_at = now(), rejection_reason = NULL, updated_at = now() WHERE vendor_id = $1 AND version = $2`, vendorID, version, actor)
	} else {
		tag, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_return_destinations SET verified_version = NULL, verified_by = NULL,
			verified_at = NULL, rejection_reason = $3, updated_at = now() WHERE vendor_id = $1 AND version = $2`, vendorID, version, reason)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleVendor
	}
	return nil
}

// TouchAddress bumps the version of a destination using the address (it
// changed, so it must be verified again). Returns whether one did.
func (r ReturnDestinationRepository) TouchAddress(ctx context.Context, addressID string) (bool, error) {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_return_destinations SET version = version + 1, updated_at = now()
		WHERE address_id = $1`, addressID)
	return err == nil && tag.RowsAffected() > 0, err
}

// UsesAddress reports whether the address is a return destination.
func (r ReturnDestinationRepository) UsesAddress(ctx context.Context, addressID string) (bool, error) {
	var used bool
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vendor_return_destinations WHERE address_id = $1)`, addressID).Scan(&used)
	return used, err
}
