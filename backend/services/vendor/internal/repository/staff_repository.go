package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/domain"
)

var (
	ErrMembershipNotFound = errors.New("repository: membership not found")
	ErrInvitationNotFound = errors.New("repository: invitation not found")
	// ErrVersionConflict: the row changed since the caller read it.
	ErrVersionConflict = errors.New("repository: version conflict")
)

// MaxInvitationAttempts parks an invitation email after this many tries.
const MaxInvitationAttempts = 6

// StaffRepository stores shop memberships, grants, invitations and the
// membership audit (AF-17).
type StaffRepository struct{ Pool *pgxpool.Pool }

const membershipColumns = `m.vendor_id::text, m.user_id::text, m.role, m.status, m.version, m.created_at, m.updated_at, m.revoked_at,
	COALESCE((SELECT array_agg(p.permission ORDER BY p.permission) FROM membership_permissions p
	          WHERE p.vendor_id = m.vendor_id AND p.user_id = m.user_id), '{}')`

func scanMembership(row pgx.Row) (*domain.Membership, error) {
	var m domain.Membership
	err := row.Scan(&m.VendorID, &m.UserID, &m.Role, &m.Status, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.RevokedAt, &m.Permissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMembershipNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// FindMembership reads one membership with its grants; inside a
// transaction the row is locked so a concurrent change waits.
func (r StaffRepository) FindMembership(ctx context.Context, vendorID, userID string) (*domain.Membership, error) {
	q := `SELECT ` + membershipColumns + ` FROM vendor_memberships m WHERE m.vendor_id = $1 AND m.user_id = $2`
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		q += ` FOR UPDATE OF m`
	}
	return scanMembership(connection(ctx, r.Pool).QueryRow(ctx, q, vendorID, userID))
}

// ListMembers lists a shop's members, owner first, revoked last.
func (r StaffRepository) ListMembers(ctx context.Context, vendorID string) ([]*domain.Membership, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+membershipColumns+` FROM vendor_memberships m WHERE m.vendor_id = $1
		ORDER BY (m.role = 'owner') DESC, (m.status = 'active') DESC, m.created_at, m.user_id LIMIT 200`, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Membership{}
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AccessibleShop is one shop the person may open in the seller console.
type AccessibleShop struct {
	Vendor     *domain.Vendor
	Membership *domain.Membership
}

// ListAccessible lists the active memberships of userID with their shops.
func (r StaffRepository) ListAccessible(ctx context.Context, userID string, staffEnabled bool) ([]AccessibleShop, error) {
	q := `SELECT ` + membershipColumns + `, v.id, v.shop_name, v.status, v.version, v.logo_url
		FROM vendor_memberships m JOIN vendors v ON v.id = m.vendor_id
		WHERE m.user_id = $1 AND m.status = 'active'`
	if !staffEnabled {
		q += ` AND m.role = 'owner'`
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, q+` ORDER BY (m.role = 'owner') DESC, v.created_at, v.id LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessibleShop{}
	for rows.Next() {
		var m domain.Membership
		var v domain.Vendor
		if err := rows.Scan(&m.VendorID, &m.UserID, &m.Role, &m.Status, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.RevokedAt, &m.Permissions,
			&v.ID, &v.ShopName, &v.Status, &v.Version, &v.LogoURL); err != nil {
			return nil, err
		}
		out = append(out, AccessibleShop{Vendor: &v, Membership: &m})
	}
	return out, rows.Err()
}

func replaceGrants(ctx context.Context, q queryer, vendorID, userID string, permissions []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM membership_permissions WHERE vendor_id = $1 AND user_id = $2`, vendorID, userID); err != nil {
		return err
	}
	if len(permissions) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `INSERT INTO membership_permissions (vendor_id, user_id, permission)
		SELECT $1, $2, unnest($3::text[])`, vendorID, userID, permissions)
	return err
}

// JoinAsStaff creates the staff membership, or reactivates a revoked one
// with the new grants and a higher version. An active row is a conflict.
func (r StaffRepository) JoinAsStaff(ctx context.Context, vendorID, userID, invitationID string, permissions []string) (*domain.Membership, error) {
	q := connection(ctx, r.Pool)
	tag, err := q.Exec(ctx, `INSERT INTO vendor_memberships (vendor_id, user_id, role, status, invitation_id)
		VALUES ($1, $2, 'staff', 'active', $3)
		ON CONFLICT (vendor_id, user_id) DO UPDATE SET status = 'active', revoked_at = NULL, version = vendor_memberships.version + 1,
			invitation_id = EXCLUDED.invitation_id, updated_at = now()
		WHERE vendor_memberships.status = 'revoked' AND vendor_memberships.role = 'staff'`, vendorID, userID, invitationID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrVersionConflict
	}
	if err := replaceGrants(ctx, q, vendorID, userID, permissions); err != nil {
		return nil, err
	}
	return r.FindMembership(ctx, vendorID, userID)
}

// UpdateGrants replaces an active staff member's permissions if the
// version still matches.
func (r StaffRepository) UpdateGrants(ctx context.Context, vendorID, userID string, expectedVersion int64, permissions []string) error {
	q := connection(ctx, r.Pool)
	tag, err := q.Exec(ctx, `UPDATE vendor_memberships SET version = version + 1, updated_at = now()
		WHERE vendor_id = $1 AND user_id = $2 AND role = 'staff' AND status = 'active' AND version = $3`, vendorID, userID, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return replaceGrants(ctx, q, vendorID, userID, permissions)
}

// RevokeMember ends a staff membership; grants are removed with it.
func (r StaffRepository) RevokeMember(ctx context.Context, vendorID, userID string, expectedVersion int64) error {
	q := connection(ctx, r.Pool)
	tag, err := q.Exec(ctx, `UPDATE vendor_memberships SET status = 'revoked', revoked_at = now(), version = version + 1, updated_at = now()
		WHERE vendor_id = $1 AND user_id = $2 AND role = 'staff' AND status = 'active' AND version = $3`, vendorID, userID, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return replaceGrants(ctx, q, vendorID, userID, nil)
}

const invitationColumns = `id::text, vendor_id::text, email_fingerprint, email_hint, permissions, status, invited_by::text, expires_at,
	accepted_user_id::text, accepted_at, delivery_status, created_at`

func scanInvitation(row pgx.Row) (*domain.Invitation, error) {
	var i domain.Invitation
	err := row.Scan(&i.ID, &i.VendorID, &i.EmailFingerprint, &i.EmailHint, &i.Permissions, &i.Status, &i.InvitedBy, &i.ExpiresAt,
		&i.AcceptedUserID, &i.AcceptedAt, &i.DeliveryStatus, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// CreateInvitation supersedes a pending invitation for the same address
// and shop, then queues the new one for delivery. It returns the ids of
// superseded invitations for the audit.
func (r StaffRepository) CreateInvitation(ctx context.Context, inv *domain.Invitation, deliveryEmail string) ([]string, error) {
	q := connection(ctx, r.Pool)
	rows, err := q.Query(ctx, `UPDATE staff_invitations SET status = 'superseded', delivery_email = NULL,
		delivery_status = 'closed', lease_until = NULL, updated_at = now()
		WHERE vendor_id = $1 AND email_fingerprint = $2 AND status = 'pending' RETURNING id::text`, inv.VendorID, inv.EmailFingerprint)
	if err != nil {
		return nil, err
	}
	superseded, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	err = q.QueryRow(ctx, `INSERT INTO staff_invitations (id, vendor_id, email_fingerprint, email_hint, delivery_email, permissions, status, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8) RETURNING delivery_status, created_at`,
		inv.ID, inv.VendorID, inv.EmailFingerprint, inv.EmailHint, deliveryEmail, inv.Permissions, inv.InvitedBy, inv.ExpiresAt).
		Scan(&inv.DeliveryStatus, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	inv.Status = domain.InvitationPending
	return superseded, nil
}

// FindInvitationByToken locks the invitation a token was issued for. The
// hash of the last token sent stays after acceptance or revocation, so a
// replayed link is recognised as used rather than unknown.
func (r StaffRepository) FindInvitationByToken(ctx context.Context, tokenHash []byte) (*domain.Invitation, error) {
	return scanInvitation(connection(ctx, r.Pool).QueryRow(ctx,
		`SELECT `+invitationColumns+` FROM staff_invitations WHERE token_hash = $1`+lockVendor(ctx), tokenHash))
}

// FindInvitation locks one invitation of a shop.
func (r StaffRepository) FindInvitation(ctx context.Context, vendorID, id string) (*domain.Invitation, error) {
	return scanInvitation(connection(ctx, r.Pool).QueryRow(ctx,
		`SELECT `+invitationColumns+` FROM staff_invitations WHERE vendor_id = $1 AND id = $2`+lockVendor(ctx), vendorID, id))
}

// ListInvitations lists a shop's recent invitations, newest first.
func (r StaffRepository) ListInvitations(ctx context.Context, vendorID string, limit int) ([]*domain.Invitation, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT `+invitationColumns+` FROM staff_invitations WHERE vendor_id = $1
		ORDER BY created_at DESC, id LIMIT $2`, vendorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Invitation{}
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// AcceptInvitation marks a pending invitation used, once.
func (r StaffRepository) AcceptInvitation(ctx context.Context, id, userID string) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE staff_invitations SET status = 'accepted', accepted_user_id = $2, accepted_at = now(),
		delivery_email = NULL, delivery_status = CASE WHEN delivery_status = 'queued' THEN 'closed' ELSE delivery_status END,
		lease_until = NULL, updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return nil
}

// RevokeInvitation closes a pending invitation; its token stops working.
func (r StaffRepository) RevokeInvitation(ctx context.Context, vendorID, id string) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE staff_invitations SET status = 'revoked', delivery_email = NULL,
		delivery_status = CASE WHEN delivery_status = 'queued' THEN 'closed' ELSE delivery_status END, lease_until = NULL, updated_at = now()
		WHERE vendor_id = $1 AND id = $2 AND status = 'pending'`, vendorID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return nil
}

// InvitationDelivery is one invitation email to send.
type InvitationDelivery struct {
	ID, VendorID, Email, ShopName string
	Attempts                      int
	ExpiresAt                     time.Time
}

// ClaimInvitationDelivery leases the next due invitation email so several
// Vendor instances never send the same one at once.
func (r StaffRepository) ClaimInvitationDelivery(ctx context.Context) (*InvitationDelivery, error) {
	var d InvitationDelivery
	err := r.Pool.QueryRow(ctx, `UPDATE staff_invitations i SET lease_until = now() + interval '1 minute', delivery_attempts = delivery_attempts + 1,
			updated_at = now()
		FROM vendors v
		WHERE v.id = i.vendor_id AND i.id = (
			SELECT id FROM staff_invitations WHERE delivery_status = 'queued' AND status = 'pending' AND next_attempt_at <= now()
				AND expires_at > now() AND (lease_until IS NULL OR lease_until < now())
			ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING i.id::text, i.vendor_id::text, COALESCE(i.delivery_email, ''), v.shop_name, i.delivery_attempts, i.expires_at`).
		Scan(&d.ID, &d.VendorID, &d.Email, &d.ShopName, &d.Attempts, &d.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SetInvitationToken stores the hash of the token about to be sent; an
// earlier token for the same invitation stops working.
func (r StaffRepository) SetInvitationToken(ctx context.Context, id string, tokenHash []byte) error {
	tag, err := r.Pool.Exec(ctx, `UPDATE staff_invitations SET token_hash = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending' AND delivery_status = 'queued'`, id, tokenHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}
	return nil
}

// FinishInvitationDelivery records the attempt: sent clears the address;
// a failure retries with backoff and parks after MaxInvitationAttempts.
func (r StaffRepository) FinishInvitationDelivery(ctx context.Context, id string, sent bool, reason string) error {
	var err error
	if sent {
		_, err = r.Pool.Exec(ctx, `UPDATE staff_invitations SET delivery_status = 'sent', delivery_email = NULL, lease_until = NULL,
			last_error = NULL, updated_at = now() WHERE id = $1 AND delivery_status = 'queued'`, id)
	} else {
		_, err = r.Pool.Exec(ctx, `UPDATE staff_invitations SET lease_until = NULL, last_error = left($2, 120),
			delivery_status = CASE WHEN delivery_attempts >= $3 THEN 'parked' ELSE delivery_status END,
			next_attempt_at = now() + least(interval '30 minutes', interval '15 seconds' * power(2, delivery_attempts)),
			updated_at = now() WHERE id = $1 AND delivery_status = 'queued'`, id, reason, MaxInvitationAttempts)
	}
	return err
}

// MembershipAudit is one append-only membership audit row.
type MembershipAudit struct {
	VendorID, ActorUserID, Action string
	MemberUserID, InvitationID    *string
	OldPermissions                []string
	NewPermissions                []string
	MembershipVersion             *int64
	Reason                        *string
}

// RecordAudit appends one audit row with the request id.
func (r StaffRepository) RecordAudit(ctx context.Context, a MembershipAudit) error {
	if a.OldPermissions == nil {
		a.OldPermissions = []string{}
	}
	if a.NewPermissions == nil {
		a.NewPermissions = []string{}
	}
	var requestID *string
	if id := middleware.RequestIDFromContext(ctx); id != "" {
		requestID = &id
	}
	_, err := connection(ctx, r.Pool).Exec(ctx, `INSERT INTO membership_audit_logs (vendor_id, member_user_id, invitation_id, actor_user_id,
		action, old_permissions, new_permissions, membership_version, reason, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		a.VendorID, a.MemberUserID, a.InvitationID, a.ActorUserID, a.Action, a.OldPermissions, a.NewPermissions, a.MembershipVersion, a.Reason, requestID)
	return err
}

// IsUniqueViolation reports a unique-constraint failure.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
