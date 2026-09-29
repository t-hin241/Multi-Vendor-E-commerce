package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

type PayoutRepository struct{ Pool *pgxpool.Pool }

const payoutColumns = `id,vendor_id,version,bank_bin,account_number_last4,status,is_default,rejection_reason,created_at,account_number_ciphertext,account_name_ciphertext`

func scanPayout(row pgx.Row) (*domain.PayoutAccount, error) {
	var a domain.PayoutAccount
	err := row.Scan(&a.ID, &a.VendorID, &a.Version, &a.BankBIN, &a.Last4, &a.Status, &a.Default, &a.RejectionReason, &a.CreatedAt, &a.NumberCipher, &a.NameCipher)
	return &a, err
}
func (r PayoutRepository) NextVersion(ctx context.Context, vendorID string) (int64, error) {
	var v int64
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM vendor_payout_accounts WHERE vendor_id=$1`, vendorID).Scan(&v)
	return v, err
}
func (r PayoutRepository) Create(ctx context.Context, a *domain.PayoutAccount) error {
	return connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO vendor_payout_accounts(id,vendor_id,version,bank_bin,account_number_last4,account_number_ciphertext,account_name_ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, a.ID, a.VendorID, a.Version, a.BankBIN, a.Last4, a.NumberCipher, a.NameCipher).Scan(&a.CreatedAt)
}
func (r PayoutRepository) Find(ctx context.Context, vendorID, id string, version int64) (*domain.PayoutAccount, error) {
	return scanPayout(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+payoutColumns+` FROM vendor_payout_accounts WHERE vendor_id=$1 AND id=$2 AND version=$3`, vendorID, id, version))
}
func (r PayoutRepository) List(ctx context.Context, vendorID string, limit, offset int) ([]*domain.PayoutAccount, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+payoutColumns+` FROM vendor_payout_accounts WHERE vendor_id=$1 ORDER BY version DESC LIMIT $2 OFFSET $3`, vendorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.PayoutAccount{}
	for rows.Next() {
		a, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r PayoutRepository) Decide(ctx context.Context, a *domain.PayoutAccount, actor, status, reason string) error {
	if status == "verified" {
		if _, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_payout_accounts SET is_default=false WHERE vendor_id=$1 AND is_default`, a.VendorID); err != nil {
			return err
		}
	}
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE vendor_payout_accounts SET status=$4,verified_by=$5,verified_at=CASE WHEN $4='verified' THEN now() ELSE NULL END,rejection_reason=NULLIF($6,''),is_default=($4='verified'),updated_at=now() WHERE vendor_id=$1 AND id=$2 AND version=$3 AND status='pending'`, a.VendorID, a.ID, a.Version, status, actor, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleVendor
	}
	return nil
}
func (r PayoutRepository) AuditRead(ctx context.Context, id, actor, scope, purpose string) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO vendor_payout_details_audit(account_id,actor_id,service_scope,purpose) VALUES($1,NULLIF($2,'')::uuid,$3,$4)`, id, actor, scope, purpose)
	return err
}
