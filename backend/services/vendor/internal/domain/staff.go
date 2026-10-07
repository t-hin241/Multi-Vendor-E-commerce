package domain

import (
	"slices"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
)

// Membership roles. The owner is the shop's creator (vendors.user_id) and
// holds every permission; staff hold exactly their granted permissions.
const (
	MemberOwner = "owner"
	MemberStaff = "staff"

	MembershipActive  = "active"
	MembershipRevoked = "revoked"

	InvitationPending    = "pending"
	InvitationAccepted   = "accepted"
	InvitationRevoked    = "revoked"
	InvitationSuperseded = "superseded"

	// InvitationTTL is how long an invitation can be accepted.
	InvitationTTL = 7 * 24 * time.Hour
)

// Membership is one person's access to one shop.
type Membership struct {
	VendorID    string
	UserID      string
	Role        string
	Status      string
	Version     int64
	Permissions []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

// Active reports whether the membership currently grants anything.
func (m *Membership) Active() bool { return m != nil && m.Status == MembershipActive }

// Has reports whether the membership grants permission. The owner holds
// every registered permission, including the owner-only ones.
func (m *Membership) Has(permission string) bool {
	if !m.Active() || !shopaccess.Known(permission) {
		return false
	}
	if m.Role == MemberOwner {
		return true
	}
	return shopaccess.Grantable(permission) && slices.Contains(m.Permissions, permission)
}

// Effective is the permission list shown to the member: all registered
// permissions for the owner, the grants for staff.
func (m *Membership) Effective() []string {
	if !m.Active() {
		return []string{}
	}
	if m.Role != MemberOwner {
		return append([]string{}, m.Permissions...)
	}
	out := []string{}
	for _, d := range shopaccess.Registry() {
		if d.Available {
			out = append(out, d.Name)
		}
	}
	return out
}

// CoveredBy reports whether every permission of m is also held by actor —
// a staff manager may only touch members it could have created itself.
func (m *Membership) CoveredBy(actor *Membership) bool {
	for _, p := range m.Permissions {
		if !actor.Has(p) {
			return false
		}
	}
	return true
}

// NormalizeGrant validates a requested permission set for staff: known,
// available, never owner-only, at least one, no duplicates; sorted.
func NormalizeGrant(permissions []string) ([]string, error) {
	if len(permissions) == 0 {
		return nil, apperror.Validation("Choose at least one permission")
	}
	if len(permissions) > 32 {
		return nil, apperror.Validation("Too many permissions")
	}
	out := make([]string, 0, len(permissions))
	for _, p := range permissions {
		p = strings.TrimSpace(p)
		if !shopaccess.Known(p) {
			return nil, apperror.Validation("Unknown permission: " + p)
		}
		if !shopaccess.Grantable(p) {
			return nil, apperror.Validation("Permission cannot be granted to staff: " + p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out, nil
}

// CheckGrantor enforces that actor may hand out every permission in grant:
// staff with staff.manage only re-grant what they hold themselves.
func CheckGrantor(actor *Membership, grant []string) error {
	if !actor.Has(shopaccess.StaffManage) {
		return shopaccess.Denied("")
	}
	for _, p := range grant {
		if !actor.Has(p) {
			return shopaccess.Denied("You can only grant permissions you hold yourself")
		}
	}
	return nil
}

// Invitation asks one email address to join a shop with a permission set.
type Invitation struct {
	ID               string
	VendorID         string
	EmailFingerprint string
	EmailHint        string
	Permissions      []string
	Status           string
	InvitedBy        string
	ExpiresAt        time.Time
	AcceptedUserID   *string
	AcceptedAt       *time.Time
	DeliveryStatus   string
	CreatedAt        time.Time
}

// Usable reports whether the invitation can still be accepted at now.
func (i *Invitation) Usable(now time.Time) bool {
	return i.Status == InvitationPending && now.Before(i.ExpiresAt)
}

// NormalizeEmail lower-cases and trims an address and checks its shape.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndex(email, "@")
	if len(email) > 254 || at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \r\n\t<>,;\"") ||
		!strings.Contains(email[at+1:], ".") {
		return "", apperror.Validation("Enter a valid email address")
	}
	return email, nil
}

// EmailHint masks an address for display: "a***@example.com".
func EmailHint(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 1 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
