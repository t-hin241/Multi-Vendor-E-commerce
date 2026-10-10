package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

var ErrReturnDestinationNotFound = errors.New("repository: return destination not found")

// ReturnDestinationRepository stores AF-05 return destinations.
type ReturnDestinationRepository struct{ Pool *pgxpool.Pool }

const returnDestinationColumns = `d.vendor_id, d.address_id, d.receiving_hours, d.version, d.verified_version, d.verified_by, d.verified_at,
	d.rejection_reason, d.updated_by, d.created_at, d.updated_at,
	d.carrier_check_version, d.carrier_check_result, d.carrier_check_reason, d.carrier_check_ref, d.carrier_checked_at,
	a.id, a.vendor_id, a.recipient_name, a.phone, a.province, a.district, a.ward, a.street_address, a.is_default, a.created_at, a.updated_at`

func scanReturnDestination(row pgx.Row) (*domain.ReturnDestination, error) {
	var d domain.ReturnDestination
	var a domain.VendorAddress
	var checkVersion *int64
	var checkResult *string
	var checkedAt *time.Time
	var check domain.CarrierAddressCheck
	err := row.Scan(&d.VendorID, &d.AddressID, &d.ReceivingHours, &d.Version, &d.VerifiedVersion, &d.VerifiedBy, &d.VerifiedAt, &d.RejectionReason,
		&d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt, &checkVersion, &checkResult, &check.Reason, &check.Reference, &checkedAt,
		&a.ID, &a.VendorID, &a.RecipientName, &a.Phone, &a.Province, &a.District, &a.Ward,
		&a.StreetAddress, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReturnDestinationNotFound
	}
	if err != nil {
		return nil, err
	}
	if checkVersion != nil && checkResult != nil && checkedAt != nil {
		check.Version, check.Result, check.CheckedAt = *checkVersion, *checkResult, *checkedAt
		d.CarrierCheck = &check
	}
	d.Address = &a
	return &d, nil
}

// Find is the shop's destination with its address; inside a transaction
// the row is locked.
func (r ReturnDestinationRepository) Find(ctx context.Context, vendorID string) (*domain.ReturnDestination, error) {
	lock := ""
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		lock = " FOR UPDATE OF d"
	}
	return scanReturnDestination(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+returnDestinationColumns+` FROM vendor_return_destinations d
		JOIN vendor_addresses a ON a.id = d.address_id WHERE d.vendor_id = $1`+lock, vendorID))
}

// PendingCarrierChecks lists destinations whose current version waits for
// a decision and was not sent to the carrier yet (PW-042), oldest first.
func (r ReturnDestinationRepository) PendingCarrierChecks(ctx context.Context, limit int) ([]*domain.ReturnDestination, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+returnDestinationColumns+` FROM vendor_return_destinations d
		JOIN vendor_addresses a ON a.id = d.address_id
		WHERE d.verified_version IS DISTINCT FROM d.version AND d.carrier_check_version IS DISTINCT FROM d.version AND d.rejection_reason IS NULL
		ORDER BY d.updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ReturnDestination
	for rows.Next() {
		d, err := scanReturnDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecordCarrierCheck stores the carrier's answer for one version; with
// decide, a deliverable answer verifies it (verified_by NULL: no person)
// and an undeliverable one rejects it with rejection.
func (r ReturnDestinationRepository) RecordCarrierCheck(ctx context.Context, vendorID string, check domain.CarrierAddressCheck, decide bool, rejection *string) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_return_destinations SET carrier_check_version = $2, carrier_check_result = $3,
			carrier_check_reason = $4, carrier_check_ref = $5, carrier_checked_at = now(),
			verified_version = CASE WHEN $6 AND $3 = 'deliverable' THEN version ELSE verified_version END,
			verified_by = CASE WHEN $6 AND $3 = 'deliverable' THEN NULL ELSE verified_by END,
			verified_at = CASE WHEN $6 AND $3 = 'deliverable' THEN now() ELSE verified_at END,
			rejection_reason = CASE WHEN $6 AND $3 = 'undeliverable' THEN $7 ELSE rejection_reason END,
			updated_at = CASE WHEN $6 AND $3 <> 'unsupported' THEN now() ELSE updated_at END
		WHERE vendor_id = $1 AND version = $2`, vendorID, check.Version, check.Result, check.Reason, check.Reference, decide, rejection)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleVendor
	}
	return nil
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
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_return_destinations SET version = version + 1, rejection_reason = NULL, updated_at = now()
		WHERE address_id = $1`, addressID)
	return err == nil && tag.RowsAffected() > 0, err
}

// UsesAddress reports whether the address is a return destination.
func (r ReturnDestinationRepository) UsesAddress(ctx context.Context, addressID string) (bool, error) {
	var used bool
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vendor_return_destinations WHERE address_id = $1)`, addressID).Scan(&used)
	return used, err
}
