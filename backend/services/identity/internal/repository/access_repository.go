package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/identity/internal/domain"
)

var (
	ErrGrantNotFound = errors.New("repository: permission grant not found")
	// ErrGrantExists: the admin already holds this bundle.
	ErrGrantExists = errors.New("repository: permission already granted")
	// ErrPermissionVersion: the admin's grants changed since the caller read them.
	ErrPermissionVersion = errors.New("repository: permission version changed")
	ErrProofInvalid      = errors.New("repository: reauthentication proof invalid")
)

// AccessRepository stores admin permission grants and reauthentication
// proofs (AF-19).
type AccessRepository struct{ Pool *pgxpool.Pool }

const grantColumns = `g.id::text, g.user_id::text, g.bundle, g.status, g.granted_by::text, g.reason, g.created_at,
	g.revoked_by::text, g.revoke_reason, g.revoked_at`

func scanGrant(row pgx.Row) (*domain.PermissionGrant, error) {
	var g domain.PermissionGrant
	err := row.Scan(&g.ID, &g.UserID, &g.Bundle, &g.Status, &g.GrantedBy, &g.Reason, &g.CreatedAt, &g.RevokedBy, &g.RevokeReason, &g.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGrantNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ActiveBundles lists the bundles userID holds now, with its permission
// version, reading the account in the same statement.
func (r AccessRepository) ActiveBundles(ctx context.Context, userID string) (*domain.User, []string, error) {
	var u domain.User
	var bundles []string
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT u.id::text, u.role, u.is_active, u.permission_version,
		COALESCE((SELECT array_agg(g.bundle ORDER BY g.bundle) FROM admin_permission_grants g WHERE g.user_id = u.id AND g.status = 'active'), '{}')
		FROM users u WHERE u.id = $1`, userID).Scan(&u.ID, &u.Role, &u.IsActive, &u.PermissionVersion, &bundles)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrUserNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return &u, bundles, nil
}

// LockSubject locks the admin whose grants change, so concurrent changes
// apply one after the other against the same version.
func (r AccessRepository) LockSubject(ctx context.Context, userID string) (*domain.User, error) {
	var u domain.User
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT id::text, email, role, is_active, permission_version FROM users WHERE id = $1`+lockUser(ctx), userID).
		Scan(&u.ID, &u.Email, &u.Role, &u.IsActive, &u.PermissionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListGrants lists grants, newest first; userID filters to one admin.
func (r AccessRepository) ListGrants(ctx context.Context, userID string, includeRevoked bool, limit int) ([]*domain.PermissionGrant, error) {
	q := `SELECT ` + grantColumns + ` FROM admin_permission_grants g WHERE ($1 = '' OR g.user_id::text = $1)`
	if !includeRevoked {
		q += ` AND g.status = 'active'`
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, q+` ORDER BY g.created_at DESC, g.id LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.PermissionGrant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FindGrant locks one grant.
func (r AccessRepository) FindGrant(ctx context.Context, id string) (*domain.PermissionGrant, error) {
	return scanGrant(connection(ctx, r.Pool).QueryRow(ctx, `SELECT `+grantColumns+` FROM admin_permission_grants g WHERE g.id = $1`+lockUser(ctx), id))
}

// bumpVersion moves the admin's permission version from expected to the next.
func bumpVersion(ctx context.Context, q queryer, userID string, expected int64) (int64, error) {
	var next int64
	err := q.QueryRow(ctx, `UPDATE users SET permission_version = permission_version + 1, updated_at = now()
		WHERE id = $1 AND permission_version = $2 RETURNING permission_version`, userID, expected).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPermissionVersion
	}
	return next, err
}

// Grant adds a bundle to an admin if its version still matches. grantedBy
// is empty only for the bootstrap command.
func (r AccessRepository) Grant(ctx context.Context, userID, bundle, grantedBy, reason string, expectedVersion int64) (*domain.PermissionGrant, int64, error) {
	q := connection(ctx, r.Pool)
	next, err := bumpVersion(ctx, q, userID, expectedVersion)
	if err != nil {
		return nil, 0, err
	}
	g, err := scanGrant(q.QueryRow(ctx, `INSERT INTO admin_permission_grants AS g (user_id, bundle, status, granted_by, reason)
		VALUES ($1, $2, 'active', NULLIF($3, '')::uuid, $4) RETURNING `+grantColumns, userID, bundle, grantedBy, reason))
	if IsUniqueViolation(err) {
		return nil, 0, ErrGrantExists
	}
	if err != nil {
		return nil, 0, err
	}
	return g, next, nil
}

// Revoke ends an active grant if the admin's version still matches.
func (r AccessRepository) Revoke(ctx context.Context, grantID, userID, revokedBy, reason string, expectedVersion int64) (int64, error) {
	q := connection(ctx, r.Pool)
	next, err := bumpVersion(ctx, q, userID, expectedVersion)
	if err != nil {
		return 0, err
	}
	tag, err := q.Exec(ctx, `UPDATE admin_permission_grants SET status = 'revoked', revoked_by = $2, revoke_reason = $3, revoked_at = now()
		WHERE id = $1 AND status = 'active'`, grantID, revokedBy, reason)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrGrantNotFound
	}
	return next, nil
}

// HasActiveBundleHolder reports whether anyone holds bundle now.
func (r AccessRepository) HasActiveBundleHolder(ctx context.Context, bundle string) (bool, error) {
	var exists bool
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_permission_grants WHERE bundle = $1 AND status = 'active')`, bundle).Scan(&exists)
	return exists, err
}

// FindAdminByEmail locks an account by email (bootstrap command).
func (r AccessRepository) FindAdminByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT id::text, email, role, is_active, permission_version FROM users WHERE email = $1`+lockUser(ctx), email).
		Scan(&u.ID, &u.Email, &u.Role, &u.IsActive, &u.PermissionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateProof stores the hash of a new proof.
func (r AccessRepository) CreateProof(ctx context.Context, proofHash []byte, userID, purpose, operationHash string, expiresAt time.Time) error {
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO reauth_proofs (proof_hash, user_id, purpose, operation_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, proofHash, userID, purpose, operationHash, expiresAt)
	return err
}

// ConsumeProof spends a proof once: it must belong to userID, match the
// purpose and operation, and not be expired or used.
func (r AccessRepository) ConsumeProof(ctx context.Context, proofHash []byte, userID, purpose, operationHash string, now time.Time) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE reauth_proofs SET consumed_at = $5
		WHERE proof_hash = $1 AND user_id = $2 AND purpose = $3 AND operation_hash = $4 AND consumed_at IS NULL AND expires_at > $5`,
		proofHash, userID, purpose, operationHash, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProofInvalid
	}
	return nil
}

// PurgeProofs deletes proofs expired for more than a day.
func (r AccessRepository) PurgeProofs(ctx context.Context, now time.Time) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM reauth_proofs WHERE expires_at < $1`, now.Add(-24*time.Hour))
	return tag.RowsAffected(), err
}

// IsUniqueViolation reports a unique-constraint failure.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
