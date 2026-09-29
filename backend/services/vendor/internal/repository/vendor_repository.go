package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/vendorsvc/internal/domain"
)

type VendorRepository struct {
	pool *pgxpool.Pool
}

func NewVendorRepository(pool *pgxpool.Pool) *VendorRepository {
	return &VendorRepository{pool: pool}
}

var ErrVendorNotFound = errors.New("repository: vendor not found")

// Create persists a new shop application. A user may own any number of
// vendors (1:N) — there is no uniqueness constraint on user_id to violate,
// so this never fails on a duplicate.
func (r *VendorRepository) Create(ctx context.Context, v *domain.Vendor) error {
	const query = `
		INSERT INTO vendors (user_id, shop_name, description)
		VALUES ($1, $2, $3)
		RETURNING id, status, created_at, updated_at, version, enforced_version`

	return connection(ctx, r.pool).QueryRow(ctx, query, v.UserID, v.ShopName, v.Description).
		Scan(&v.ID, &v.Status, &v.CreatedAt, &v.UpdatedAt, &v.Version, &v.EnforcedVersion)
}

// ListByUserID returns every shop (any status) a user owns — a user may
// own several under the 1:N vendor↔user relationship.
func (r *VendorRepository) ListByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Vendor, error) {
	const query = vendorSelectColumns + `FROM vendors WHERE user_id = $1 ORDER BY created_at ASC,id ASC LIMIT $2 OFFSET $3`
	rows, err := connection(ctx, r.pool).Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vendors []*domain.Vendor
	for rows.Next() {
		v, err := scanVendorRow(rows)
		if err != nil {
			return nil, err
		}
		vendors = append(vendors, v)
	}
	return vendors, rows.Err()
}

func (r *VendorRepository) FindByID(ctx context.Context, id string) (*domain.Vendor, error) {
	const query = vendorSelectColumns + `FROM vendors WHERE id = $1`
	return scanVendor(connection(ctx, r.pool).QueryRow(ctx, query+lockVendor(ctx), id))
}

func (r *VendorRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.Vendor, error) {
	var (
		rows pgx.Rows
		err  error
	)

	if status == "" {
		rows, err = connection(ctx, r.pool).Query(ctx, vendorSelectColumns+`FROM vendors ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = connection(ctx, r.pool).Query(ctx, vendorSelectColumns+`FROM vendors WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vendors []*domain.Vendor
	for rows.Next() {
		v, err := scanVendorRow(rows)
		if err != nil {
			return nil, err
		}
		vendors = append(vendors, v)
	}
	return vendors, rows.Err()
}

// ListByIDs batch-looks-up vendors by id, for services (Catalog) that need
// to resolve a shop name for many vendor ids at once without one request
// per id. Any id not found is simply absent from the result.
func (r *VendorRepository) ListByIDs(ctx context.Context, ids []string) ([]*domain.Vendor, error) {
	const query = vendorSelectColumns + `FROM vendors WHERE id = ANY($1)`
	rows, err := connection(ctx, r.pool).Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vendors []*domain.Vendor
	for rows.Next() {
		v, err := scanVendorRow(rows)
		if err != nil {
			return nil, err
		}
		vendors = append(vendors, v)
	}
	return vendors, rows.Err()
}

func (r *VendorRepository) UpdateProfile(ctx context.Context, id, shopName, description, policyText string) error {
	const query = `UPDATE vendors SET shop_name = $1, description = $2, policy_text = $3, updated_at = now() WHERE id = $4`
	tag, err := connection(ctx, r.pool).Exec(ctx, query, shopName, description, policyText, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorNotFound
	}
	return nil
}

func (r *VendorRepository) SetLogo(ctx context.Context, id string, url, objectKey *string) error {
	const query = `UPDATE vendors SET logo_url = $1, logo_object_key = $2, updated_at = now() WHERE id = $3`
	tag, err := connection(ctx, r.pool).Exec(ctx, query, url, objectKey, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorNotFound
	}
	return nil
}

func (r *VendorRepository) SetBanner(ctx context.Context, id string, url, objectKey *string) error {
	const query = `UPDATE vendors SET banner_url = $1, banner_object_key = $2, updated_at = now() WHERE id = $3`
	tag, err := connection(ctx, r.pool).Exec(ctx, query, url, objectKey, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVendorNotFound
	}
	return nil
}

var ErrStaleVendor = errors.New("vendor version changed")

func (r *VendorRepository) UpdateStatus(ctx context.Context, id string, version int64, status domain.Status, actor string, reason *string) error {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE vendors SET status=$1,version=version+1,
 approved_by=CASE WHEN $1='approved' THEN $2::uuid ELSE approved_by END,
 approved_at=CASE WHEN $1='approved' THEN now() ELSE approved_at END,
 rejection_reason=CASE WHEN $1='rejected' THEN $3 ELSE NULL END,
 suspension_reason=CASE WHEN $1='suspended' THEN $3 ELSE NULL END,updated_at=now()
 WHERE id=$4 AND version=$5`, status, actor, reason, id, version)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrStaleVendor
	}
	return err
}
func (r *VendorRepository) Snapshots(ctx context.Context, after string, limit int) ([]*domain.Vendor, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, vendorSelectColumns+`FROM vendors WHERE ($1='' OR id>NULLIF($1,'')::uuid) ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Vendor{}
	for rows.Next() {
		v, e := scanVendorRow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const vendorSelectColumns = `
	SELECT id, user_id, shop_name, description, status, rejection_reason, approved_by, approved_at,
	       logo_url, logo_object_key, banner_url, banner_object_key, policy_text, created_at, updated_at, version, enforced_version, suspension_reason
	`

func scanVendor(row pgx.Row) (*domain.Vendor, error) {
	var v domain.Vendor
	err := row.Scan(&v.ID, &v.UserID, &v.ShopName, &v.Description, &v.Status, &v.RejectionReason, &v.ApprovedBy, &v.ApprovedAt,
		&v.LogoURL, &v.LogoObjectKey, &v.BannerURL, &v.BannerObjectKey, &v.PolicyText, &v.CreatedAt, &v.UpdatedAt, &v.Version, &v.EnforcedVersion, &v.SuspensionReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVendorNotFound
		}
		return nil, err
	}
	return &v, nil
}

func scanVendorRow(rows pgx.Rows) (*domain.Vendor, error) {
	var v domain.Vendor
	err := rows.Scan(&v.ID, &v.UserID, &v.ShopName, &v.Description, &v.Status, &v.RejectionReason, &v.ApprovedBy, &v.ApprovedAt,
		&v.LogoURL, &v.LogoObjectKey, &v.BannerURL, &v.BannerObjectKey, &v.PolicyText, &v.CreatedAt, &v.UpdatedAt, &v.Version, &v.EnforcedVersion, &v.SuspensionReason)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
