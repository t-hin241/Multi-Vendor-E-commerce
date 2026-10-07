package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// StaffStore stores memberships, grants, invitations and their audit.
type StaffStore interface {
	FindMembership(ctx context.Context, vendorID, userID string) (*domain.Membership, error)
	ListMembers(ctx context.Context, vendorID string) ([]*domain.Membership, error)
	ListAccessible(ctx context.Context, userID string, staffEnabled bool) ([]repository.AccessibleShop, error)
	JoinAsStaff(ctx context.Context, vendorID, userID, invitationID string, permissions []string) (*domain.Membership, error)
	UpdateGrants(ctx context.Context, vendorID, userID string, expectedVersion int64, permissions []string) error
	RevokeMember(ctx context.Context, vendorID, userID string, expectedVersion int64) error
	CreateInvitation(ctx context.Context, inv *domain.Invitation, deliveryEmail string) ([]string, error)
	FindInvitationByToken(ctx context.Context, tokenHash []byte) (*domain.Invitation, error)
	FindInvitation(ctx context.Context, vendorID, id string) (*domain.Invitation, error)
	ListInvitations(ctx context.Context, vendorID string, limit int) ([]*domain.Invitation, error)
	AcceptInvitation(ctx context.Context, id, userID string) error
	RevokeInvitation(ctx context.Context, vendorID, id string) error
	ClaimInvitationDelivery(ctx context.Context) (*repository.InvitationDelivery, error)
	SetInvitationToken(ctx context.Context, id string, tokenHash []byte) error
	FinishInvitationDelivery(ctx context.Context, id string, sent bool, reason string) error
	RecordAudit(ctx context.Context, a repository.MembershipAudit) error
}

// AccountDirectory confirms an account with Identity.
type AccountDirectory interface {
	Account(ctx context.Context, userID string) (identityclient.Account, error)
}

// InvitationMail is one invitation email. URL carries the token in its
// fragment, so it never reaches a server log or analytics.
type InvitationMail struct {
	DeliveryID, Email, ShopName, URL string
	ExpiresAt                        time.Time
}

// InvitationMailer hands an invitation email to Notification, which sends
// it without storing it.
type InvitationMailer interface {
	SendInvitation(ctx context.Context, mail InvitationMail) error
}

// StaffUseCase owns shop memberships (AF-17): who may act for a shop and
// with which permissions, invitations, and the authorize contract other
// services call on every seller request.
type StaffUseCase struct {
	Staff    StaffStore
	Vendors  VendorRepositoryPort
	Accounts AccountDirectory
	Tx       Transactions
	Mailer   InvitationMailer
	// Enabled is FEATURE_SHOP_STAFF_ENABLED. Off, only owners pass and no
	// invitation is created or accepted; owners can still remove staff.
	Enabled bool
	// InvitesPaused (SHOP_STAFF_INVITES_PAUSED) stops new invitations and
	// added permissions while existing staff keep working.
	InvitesPaused  bool
	FingerprintKey []byte
	AcceptURL      string
	Now            func() time.Time
	Log            zerolog.Logger
}

func (uc *StaffUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func staffError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Message: message, Status: status}
}

var (
	errStaffDisabled = staffError(http.StatusNotFound, "feature_disabled", "Shop staff is not enabled")
	errInvitesPaused = staffError(http.StatusConflict, "invitations_paused", "New invitations and permissions are paused")
)

// AuthorizeResult answers POST /internal/vendors/authorize.
type AuthorizeResult struct {
	Allowed           bool
	VendorID          string
	Status            string
	VendorVersion     int64
	Role              string
	MembershipVersion int64
}

// Authorize answers whether actorID holds permission on vendorID right
// now. It reads the membership on every call (no cache), and a doubt about
// the account is an error, never a yes.
func (uc *StaffUseCase) Authorize(ctx context.Context, actorID, vendorID, permission string) (*AuthorizeResult, error) {
	if !shopaccess.Known(permission) {
		return nil, apperror.Validation("Unknown permission")
	}
	if _, err := uuid.Parse(actorID); err != nil {
		return nil, apperror.Validation("Invalid actor")
	}
	if _, err := uuid.Parse(vendorID); err != nil {
		return nil, apperror.Validation("Invalid shop ID")
	}
	denied := &AuthorizeResult{VendorID: vendorID}
	v, err := uc.Vendors.FindByID(ctx, vendorID)
	if errors.Is(err, repository.ErrVendorNotFound) {
		return denied, nil
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	m, err := uc.Staff.FindMembership(ctx, vendorID, actorID)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return denied, nil
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if (m.Role == domain.MemberStaff && !uc.Enabled) || !m.Has(permission) {
		return denied, nil
	}
	if ok, err := uc.accountMayAct(ctx, actorID, m.Role); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return denied, nil
	}
	return &AuthorizeResult{Allowed: true, VendorID: v.ID, Status: string(v.Status), VendorVersion: v.Version,
		Role: m.Role, MembershipVersion: m.Version}, nil
}

// accountMayAct: the owner keeps needing an active vendor account (as
// before AF-17); staff need an active buyer or vendor account.
func (uc *StaffUseCase) accountMayAct(ctx context.Context, userID, role string) (bool, error) {
	acct, err := uc.Accounts.Account(ctx, userID)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == apperror.CodeForbidden {
			return false, nil
		}
		return false, err
	}
	if !acct.Active {
		return false, nil
	}
	if role == domain.MemberOwner {
		return acct.Role == "vendor", nil
	}
	return acct.Role == "vendor" || acct.Role == "buyer", nil
}

// member returns actorID's usable membership of vendorID or a refusal.
func (uc *StaffUseCase) member(ctx context.Context, actorID, vendorID string) (*domain.Membership, error) {
	m, err := uc.Staff.FindMembership(ctx, vendorID, actorID)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return nil, shopaccess.Denied("")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !m.Active() || (m.Role == domain.MemberStaff && !uc.Enabled) {
		return nil, shopaccess.Denied("")
	}
	ok, err := uc.accountMayAct(ctx, actorID, m.Role)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, shopaccess.Denied("")
	}
	return m, nil
}

// Require is Authorize for Vendor's own seller endpoints.
func (uc *StaffUseCase) Require(ctx context.Context, actorID, vendorID, permission string) (*domain.Membership, error) {
	m, err := uc.member(ctx, actorID, vendorID)
	if err != nil {
		return nil, err
	}
	if !m.Has(permission) {
		return nil, shopaccess.Denied("")
	}
	return m, nil
}

// GetForMember returns the shop to any active member (the console header).
func (uc *StaffUseCase) GetForMember(ctx context.Context, actorID, vendorID string) (*domain.Vendor, *domain.Membership, error) {
	m, err := uc.member(ctx, actorID, vendorID)
	if err != nil {
		return nil, nil, err
	}
	v, err := uc.Vendors.FindByID(ctx, vendorID)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	return v, m, nil
}

// AccessibleShops lists the shops actorID may open, with capabilities.
// It is read from memberships, never inferred from the token's role.
func (uc *StaffUseCase) AccessibleShops(ctx context.Context, actorID string) ([]repository.AccessibleShop, error) {
	acct, err := uc.Accounts.Account(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if !acct.Active {
		return []repository.AccessibleShop{}, nil
	}
	shops, err := uc.Staff.ListAccessible(ctx, actorID, uc.Enabled)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	out := shops[:0]
	for _, s := range shops {
		if s.Membership.Role == domain.MemberOwner && acct.Role != "vendor" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// Fingerprint is the keyed hash an invitation stores instead of the address.
func (uc *StaffUseCase) Fingerprint(email string) string {
	mac := hmac.New(sha256.New, uc.FingerprintKey)
	mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil))
}

// InviteInput is an owner's or staff manager's invitation.
type InviteInput struct {
	Email       string
	Permissions []string
}

// Invite queues an invitation for an address. The response never says
// whether an account exists for it; acceptance checks that.
func (uc *StaffUseCase) Invite(ctx context.Context, actorID, vendorID string, in InviteInput) (*domain.Invitation, error) {
	if !uc.Enabled {
		return nil, errStaffDisabled
	}
	if uc.InvitesPaused {
		return nil, errInvitesPaused
	}
	if len(uc.FingerprintKey) == 0 || uc.AcceptURL == "" {
		return nil, apperror.Internal(errors.New("staff invitations are not configured"))
	}
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	grant, err := domain.NormalizeGrant(in.Permissions)
	if err != nil {
		return nil, err
	}
	acct, err := uc.Accounts.Account(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(acct.Email), email) {
		return nil, apperror.Validation("You already belong to this shop")
	}
	inv := &domain.Invitation{ID: uuid.NewString(), VendorID: vendorID, EmailFingerprint: uc.Fingerprint(email), EmailHint: domain.EmailHint(email),
		Permissions: grant, InvitedBy: actorID, ExpiresAt: uc.now().Add(domain.InvitationTTL)}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		actor, err := uc.member(ctx, actorID, vendorID)
		if err != nil {
			return err
		}
		if err := domain.CheckGrantor(actor, grant); err != nil {
			return err
		}
		superseded, err := uc.Staff.CreateInvitation(ctx, inv, email)
		if err != nil {
			return err
		}
		for _, id := range superseded {
			reason := "superseded by a new invitation"
			if err := uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: vendorID, ActorUserID: actorID, Action: "staff_invitation_revoked",
				InvitationID: &id, Reason: &reason}); err != nil {
				return err
			}
		}
		return uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: vendorID, ActorUserID: actorID, Action: "staff_invited",
			InvitationID: &inv.ID, NewPermissions: grant})
	})
	if err != nil {
		return nil, wrap(err)
	}
	return inv, nil
}

// ListInvitations lists a shop's invitations for a staff manager.
func (uc *StaffUseCase) ListInvitations(ctx context.Context, actorID, vendorID string) ([]*domain.Invitation, error) {
	if _, err := uc.Require(ctx, actorID, vendorID, shopaccess.StaffManage); err != nil {
		return nil, err
	}
	out, err := uc.Staff.ListInvitations(ctx, vendorID, 100)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return out, nil
}

// RevokeInvitation closes a pending invitation; its link stops working.
// It works with the feature off, so access can always be taken back.
func (uc *StaffUseCase) RevokeInvitation(ctx context.Context, actorID, vendorID, invitationID string, reason *string) error {
	if err := validReason(reason); err != nil {
		return err
	}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		actor, err := uc.manager(ctx, actorID, vendorID)
		if err != nil {
			return err
		}
		inv, err := uc.Staff.FindInvitation(ctx, vendorID, invitationID)
		if errors.Is(err, repository.ErrInvitationNotFound) {
			return apperror.NotFound("Invitation not found")
		}
		if err != nil {
			return err
		}
		if inv.Status != domain.InvitationPending {
			return staffError(http.StatusConflict, "invitation_used", "This invitation is no longer pending")
		}
		if actor.Role != domain.MemberOwner && !(&domain.Membership{Permissions: inv.Permissions}).CoveredBy(actor) {
			return shopaccess.Denied("You can only revoke invitations within your own permissions")
		}
		if err := uc.Staff.RevokeInvitation(ctx, vendorID, invitationID); err != nil {
			return err
		}
		return uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: vendorID, ActorUserID: actorID, Action: "staff_invitation_revoked",
			InvitationID: &invitationID, OldPermissions: inv.Permissions, Reason: reason})
	})
	return wrap(err)
}

// manager is a member allowed to take access back: the owner, or staff
// with staff.manage. Unlike member, it does not need the feature on, so
// an owner can always clean up.
func (uc *StaffUseCase) manager(ctx context.Context, actorID, vendorID string) (*domain.Membership, error) {
	m, err := uc.Staff.FindMembership(ctx, vendorID, actorID)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return nil, shopaccess.Denied("")
	}
	if err != nil {
		return nil, err
	}
	if !m.Active() || (m.Role == domain.MemberStaff && !uc.Enabled) || !m.Has(shopaccess.StaffManage) {
		return nil, shopaccess.Denied("")
	}
	ok, err := uc.accountMayAct(ctx, actorID, m.Role)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, shopaccess.Denied("")
	}
	return m, nil
}

// TokenHash hashes an invitation token from its link; a malformed token
// has no hash.
func TokenHash(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

// AcceptInvitation joins actorID to the shop named by the token. The
// signed-in account's address must be the invited one, so a forwarded
// link is useless to anyone else; a token works once.
func (uc *StaffUseCase) AcceptInvitation(ctx context.Context, actorID, token string) (*domain.Membership, error) {
	if !uc.Enabled {
		return nil, errStaffDisabled
	}
	hash, ok := TokenHash(token)
	if !ok {
		return nil, apperror.NotFound("Invitation not found or no longer valid")
	}
	acct, err := uc.Accounts.Account(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if !acct.Active || (acct.Role != "buyer" && acct.Role != "vendor") {
		return nil, shopaccess.Denied("This account cannot join a shop")
	}
	email, err := domain.NormalizeEmail(acct.Email)
	if err != nil {
		return nil, shopaccess.Denied("This invitation was sent to a different email address")
	}
	var joined *domain.Membership
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		inv, err := uc.Staff.FindInvitationByToken(ctx, hash)
		if errors.Is(err, repository.ErrInvitationNotFound) {
			return apperror.NotFound("Invitation not found or no longer valid")
		}
		if err != nil {
			return err
		}
		switch {
		case inv.Status == domain.InvitationAccepted:
			return staffError(http.StatusConflict, "invitation_used", "This invitation has already been used")
		case inv.Status != domain.InvitationPending:
			return staffError(http.StatusConflict, "invitation_revoked", "This invitation was withdrawn")
		case !inv.Usable(uc.now()):
			return staffError(http.StatusConflict, "invitation_expired", "This invitation has expired; ask the shop for a new one")
		}
		if !hmac.Equal([]byte(uc.Fingerprint(email)), []byte(inv.EmailFingerprint)) {
			return shopaccess.Denied("This invitation was sent to a different email address")
		}
		// The inviter must still be able to grant this today: revoking a
		// manager also stops the invitations it sent.
		inviter, err := uc.Staff.FindMembership(ctx, inv.VendorID, inv.InvitedBy)
		if err != nil && !errors.Is(err, repository.ErrMembershipNotFound) {
			return err
		}
		if inviter == nil || domain.CheckGrantor(inviter, inv.Permissions) != nil {
			return staffError(http.StatusConflict, "invitation_revoked", "This invitation is no longer valid; ask the shop for a new one")
		}
		existing, err := uc.Staff.FindMembership(ctx, inv.VendorID, actorID)
		if err != nil && !errors.Is(err, repository.ErrMembershipNotFound) {
			return err
		}
		if existing.Active() {
			return staffError(http.StatusConflict, "already_member", "You already belong to this shop")
		}
		joined, err = uc.Staff.JoinAsStaff(ctx, inv.VendorID, actorID, inv.ID, inv.Permissions)
		if err != nil {
			return err
		}
		if err := uc.Staff.AcceptInvitation(ctx, inv.ID, actorID); err != nil {
			return err
		}
		return uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: inv.VendorID, ActorUserID: actorID, Action: "staff_joined",
			MemberUserID: &actorID, InvitationID: &inv.ID, NewPermissions: joined.Permissions, MembershipVersion: &joined.Version})
	})
	if err != nil {
		return nil, wrap(err)
	}
	return joined, nil
}

// MemberView is a member with the account details Identity confirms.
type MemberView struct {
	*domain.Membership
	Email string
}

// ListMembers lists a shop's members for a staff manager.
func (uc *StaffUseCase) ListMembers(ctx context.Context, actorID, vendorID string) ([]MemberView, error) {
	if _, err := uc.manager(ctx, actorID, vendorID); err != nil {
		return nil, err
	}
	members, err := uc.Staff.ListMembers(ctx, vendorID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	out := make([]MemberView, 0, len(members))
	for _, m := range members {
		view := MemberView{Membership: m}
		if acct, err := uc.Accounts.Account(ctx, m.UserID); err == nil {
			view.Email = acct.Email
		}
		out = append(out, view)
	}
	return out, nil
}

// GetMember returns one member to a staff manager, or to the member.
func (uc *StaffUseCase) GetMember(ctx context.Context, actorID, vendorID, userID string) (*domain.Membership, error) {
	if actorID != userID {
		if _, err := uc.manager(ctx, actorID, vendorID); err != nil {
			return nil, err
		}
	} else if _, err := uc.member(ctx, actorID, vendorID); err != nil {
		return nil, err
	}
	m, err := uc.Staff.FindMembership(ctx, vendorID, userID)
	if errors.Is(err, repository.ErrMembershipNotFound) {
		return nil, apperror.NotFound("Member not found")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return m, nil
}

// MemberChange is a PATCH of a member's permissions.
type MemberChange struct {
	Permissions     []string
	ExpectedVersion int64
	Reason          *string
}

var (
	errLastOwner      = staffError(http.StatusConflict, "last_owner", "The shop owner cannot be removed or changed")
	errMemberChanged  = staffError(http.StatusConflict, "version_conflict", "This member changed meanwhile; reload and try again")
	errNoSelfChange   = shopaccess.Denied("You cannot change your own permissions")
	errVersionMissing = apperror.Validation("expected_version is required")
)

// UpdateMember replaces a staff member's permissions. No one changes
// their own permissions, and a staff manager only grants and touches
// what it holds itself.
func (uc *StaffUseCase) UpdateMember(ctx context.Context, actorID, vendorID, userID string, in MemberChange) (*domain.Membership, error) {
	if !uc.Enabled {
		return nil, errStaffDisabled
	}
	if in.ExpectedVersion < 1 {
		return nil, errVersionMissing
	}
	if err := validReason(in.Reason); err != nil {
		return nil, err
	}
	grant, err := domain.NormalizeGrant(in.Permissions)
	if err != nil {
		return nil, err
	}
	if actorID == userID {
		return nil, errNoSelfChange
	}
	var out *domain.Membership
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		actor, err := uc.manager(ctx, actorID, vendorID)
		if err != nil {
			return err
		}
		target, err := uc.Staff.FindMembership(ctx, vendorID, userID)
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return apperror.NotFound("Member not found")
		}
		if err != nil {
			return err
		}
		if target.Role == domain.MemberOwner {
			return errLastOwner
		}
		if !target.Active() || target.Version != in.ExpectedVersion {
			return errMemberChanged
		}
		if err := domain.CheckGrantor(actor, grant); err != nil {
			return err
		}
		if actor.Role != domain.MemberOwner && !target.CoveredBy(actor) {
			return shopaccess.Denied("This member holds permissions you do not have")
		}
		if uc.InvitesPaused {
			for _, p := range grant {
				if !slices.Contains(target.Permissions, p) {
					return errInvitesPaused
				}
			}
		}
		if err := uc.Staff.UpdateGrants(ctx, vendorID, userID, in.ExpectedVersion, grant); err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				return errMemberChanged
			}
			return err
		}
		version := in.ExpectedVersion + 1
		if err := uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: vendorID, ActorUserID: actorID, Action: "staff_permissions_changed",
			MemberUserID: &userID, OldPermissions: target.Permissions, NewPermissions: grant, MembershipVersion: &version, Reason: in.Reason}); err != nil {
			return err
		}
		out, err = uc.Staff.FindMembership(ctx, vendorID, userID)
		return err
	})
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// RemoveMember revokes a staff membership: the next request of that person
// is refused. A member may leave on its own; the owner is never removed.
// It works with the feature off.
func (uc *StaffUseCase) RemoveMember(ctx context.Context, actorID, vendorID, userID string, expectedVersion int64, reason *string) error {
	if expectedVersion < 1 {
		return errVersionMissing
	}
	if err := validReason(reason); err != nil {
		return err
	}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		target, err := uc.Staff.FindMembership(ctx, vendorID, userID)
		if errors.Is(err, repository.ErrMembershipNotFound) {
			return apperror.NotFound("Member not found")
		}
		if err != nil {
			return err
		}
		if actorID != userID {
			actor, err := uc.manager(ctx, actorID, vendorID)
			if err != nil {
				return err
			}
			if target.Role != domain.MemberOwner && actor.Role != domain.MemberOwner && !target.CoveredBy(actor) {
				return shopaccess.Denied("This member holds permissions you do not have")
			}
		}
		if target.Role == domain.MemberOwner {
			return errLastOwner
		}
		if !target.Active() || target.Version != expectedVersion {
			return errMemberChanged
		}
		if err := uc.Staff.RevokeMember(ctx, vendorID, userID, expectedVersion); err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				return errMemberChanged
			}
			return err
		}
		version := expectedVersion + 1
		return uc.Staff.RecordAudit(ctx, repository.MembershipAudit{VendorID: vendorID, ActorUserID: actorID, Action: "staff_revoked",
			MemberUserID: &userID, OldPermissions: target.Permissions, MembershipVersion: &version, Reason: reason})
	})
	return wrap(err)
}

func validReason(reason *string) error {
	if reason != nil && len(*reason) > 500 {
		return apperror.Validation("Reason must be at most 500 characters")
	}
	return nil
}

// RunInvitationDelivery sends queued invitation emails until ctx ends.
func (uc *StaffUseCase) RunInvitationDelivery(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for i := 0; i < 20 && uc.DeliverNextInvitation(ctx); i++ {
		}
	}
}

// DeliverNextInvitation sends one due invitation; it reports whether one
// was claimed. A fresh token is issued per attempt and only its hash is
// stored, so neither Vendor nor Notification keeps a usable link.
func (uc *StaffUseCase) DeliverNextInvitation(ctx context.Context) bool {
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	d, err := uc.Staff.ClaimInvitationDelivery(jobCtx)
	if err != nil {
		uc.Log.Error().Msg("staff_invitation_claim_failed")
		return false
	}
	if d == nil {
		return false
	}
	reason := ""
	if d.Email == "" || uc.Mailer == nil || uc.AcceptURL == "" {
		reason = "delivery not configured"
	} else {
		reason = uc.send(jobCtx, d)
	}
	if reason == "closed" {
		return true
	}
	if err := uc.Staff.FinishInvitationDelivery(jobCtx, d.ID, reason == "", reason); err != nil {
		uc.Log.Error().Str("invitation_id", d.ID).Msg("staff_invitation_persist_failed")
	}
	if reason != "" {
		uc.Log.Warn().Str("invitation_id", d.ID).Str("vendor_id", d.VendorID).Int("attempt", d.Attempts).Str("reason", reason).
			Bool("parked", d.Attempts >= repository.MaxInvitationAttempts).Msg("staff_invitation_delivery_pending")
	}
	return true
}

func (uc *StaffUseCase) send(ctx context.Context, d *repository.InvitationDelivery) string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "token generation failed"
	}
	sum := sha256.Sum256(raw)
	if err := uc.Staff.SetInvitationToken(ctx, d.ID, sum[:]); err != nil {
		if errors.Is(err, repository.ErrVersionConflict) {
			return "closed"
		}
		return "token not stored"
	}
	link, err := url.Parse(uc.AcceptURL)
	if err != nil {
		return "invalid accept URL"
	}
	link.Fragment = "token=" + base64.RawURLEncoding.EncodeToString(raw)
	if err := uc.Mailer.SendInvitation(ctx, InvitationMail{DeliveryID: d.ID, Email: d.Email, ShopName: d.ShopName, URL: link.String(),
		ExpiresAt: d.ExpiresAt}); err != nil {
		return "notification unavailable"
	}
	return ""
}
