package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/identityclient"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type fakeStaffStore struct {
	members     map[string]*domain.Membership
	invitations map[string]*domain.Invitation
	emails      map[string]string
	tokens      map[string]string
	audit       []repository.MembershipAudit
	queued      []string
}

func newFakeStaffStore() *fakeStaffStore {
	return &fakeStaffStore{members: map[string]*domain.Membership{}, invitations: map[string]*domain.Invitation{},
		emails: map[string]string{}, tokens: map[string]string{}}
}

func key(vendorID, userID string) string { return vendorID + "|" + userID }

func (f *fakeStaffStore) FindMembership(_ context.Context, vendorID, userID string) (*domain.Membership, error) {
	m, ok := f.members[key(vendorID, userID)]
	if !ok {
		return nil, repository.ErrMembershipNotFound
	}
	copied := *m
	copied.Permissions = slices.Clone(m.Permissions)
	return &copied, nil
}
func (f *fakeStaffStore) ListMembers(_ context.Context, vendorID string) ([]*domain.Membership, error) {
	var out []*domain.Membership
	for _, m := range f.members {
		if m.VendorID == vendorID {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeStaffStore) ListAccessible(_ context.Context, userID string, staff bool) ([]repository.AccessibleShop, error) {
	var out []repository.AccessibleShop
	for _, m := range f.members {
		if m.UserID == userID && m.Active() && (staff || m.Role == domain.MemberOwner) {
			out = append(out, repository.AccessibleShop{Vendor: &domain.Vendor{ID: m.VendorID}, Membership: m})
		}
	}
	return out, nil
}
func (f *fakeStaffStore) JoinAsStaff(_ context.Context, vendorID, userID, _ string, permissions []string) (*domain.Membership, error) {
	m, ok := f.members[key(vendorID, userID)]
	if ok && m.Active() {
		return nil, repository.ErrVersionConflict
	}
	if !ok {
		m = &domain.Membership{VendorID: vendorID, UserID: userID, Role: domain.MemberStaff}
		f.members[key(vendorID, userID)] = m
	}
	m.Status, m.Version, m.Permissions, m.RevokedAt = domain.MembershipActive, m.Version+1, slices.Clone(permissions), nil
	return f.FindMembership(context.Background(), vendorID, userID)
}
func (f *fakeStaffStore) UpdateGrants(_ context.Context, vendorID, userID string, version int64, permissions []string) error {
	m, ok := f.members[key(vendorID, userID)]
	if !ok || m.Version != version || !m.Active() {
		return repository.ErrVersionConflict
	}
	m.Version++
	m.Permissions = slices.Clone(permissions)
	return nil
}
func (f *fakeStaffStore) RevokeMember(_ context.Context, vendorID, userID string, version int64) error {
	m, ok := f.members[key(vendorID, userID)]
	if !ok || m.Version != version || !m.Active() || m.Role != domain.MemberStaff {
		return repository.ErrVersionConflict
	}
	now := time.Now()
	m.Version, m.Status, m.Permissions, m.RevokedAt = m.Version+1, domain.MembershipRevoked, nil, &now
	return nil
}
func (f *fakeStaffStore) CreateInvitation(_ context.Context, inv *domain.Invitation, email string) ([]string, error) {
	var superseded []string
	for _, other := range f.invitations {
		if other.VendorID == inv.VendorID && other.EmailFingerprint == inv.EmailFingerprint && other.Status == domain.InvitationPending {
			other.Status = domain.InvitationSuperseded
			superseded = append(superseded, other.ID)
		}
	}
	inv.Status, inv.DeliveryStatus = domain.InvitationPending, "queued"
	copied := *inv
	f.invitations[inv.ID] = &copied
	f.emails[inv.ID] = email
	f.queued = append(f.queued, inv.ID)
	return superseded, nil
}
func (f *fakeStaffStore) FindInvitationByToken(_ context.Context, hash []byte) (*domain.Invitation, error) {
	id, ok := f.tokens[string(hash)]
	if !ok {
		return nil, repository.ErrInvitationNotFound
	}
	copied := *f.invitations[id]
	return &copied, nil
}
func (f *fakeStaffStore) FindInvitation(_ context.Context, vendorID, id string) (*domain.Invitation, error) {
	inv, ok := f.invitations[id]
	if !ok || inv.VendorID != vendorID {
		return nil, repository.ErrInvitationNotFound
	}
	copied := *inv
	return &copied, nil
}
func (f *fakeStaffStore) ListInvitations(_ context.Context, vendorID string, _ int) ([]*domain.Invitation, error) {
	var out []*domain.Invitation
	for _, inv := range f.invitations {
		if inv.VendorID == vendorID {
			out = append(out, inv)
		}
	}
	return out, nil
}
func (f *fakeStaffStore) AcceptInvitation(_ context.Context, id, userID string) error {
	inv := f.invitations[id]
	if inv.Status != domain.InvitationPending {
		return repository.ErrVersionConflict
	}
	now := time.Now()
	inv.Status, inv.AcceptedUserID, inv.AcceptedAt = domain.InvitationAccepted, &userID, &now
	return nil
}
func (f *fakeStaffStore) RevokeInvitation(_ context.Context, vendorID, id string) error {
	inv, ok := f.invitations[id]
	if !ok || inv.VendorID != vendorID || inv.Status != domain.InvitationPending {
		return repository.ErrVersionConflict
	}
	inv.Status = domain.InvitationRevoked
	return nil
}
func (f *fakeStaffStore) ClaimInvitationDelivery(context.Context) (*repository.InvitationDelivery, error) {
	if len(f.queued) == 0 {
		return nil, nil
	}
	id := f.queued[0]
	f.queued = f.queued[1:]
	inv := f.invitations[id]
	return &repository.InvitationDelivery{ID: id, VendorID: inv.VendorID, Email: f.emails[id], ShopName: "Test shop", Attempts: 1,
		ExpiresAt: inv.ExpiresAt}, nil
}
func (f *fakeStaffStore) SetInvitationToken(_ context.Context, id string, hash []byte) error {
	for k, v := range f.tokens {
		if v == id {
			delete(f.tokens, k)
		}
	}
	f.tokens[string(hash)] = id
	return nil
}
func (f *fakeStaffStore) FinishInvitationDelivery(_ context.Context, id string, sent bool, _ string) error {
	if sent {
		f.invitations[id].DeliveryStatus = "sent"
		delete(f.emails, id)
	} else {
		f.queued = append(f.queued, id)
	}
	return nil
}
func (f *fakeStaffStore) RecordAudit(_ context.Context, a repository.MembershipAudit) error {
	if strings.Contains(strings.Join(a.NewPermissions, ","), "@") {
		return errors.New("audit must not carry an address")
	}
	f.audit = append(f.audit, a)
	return nil
}

type fakeAccounts map[string]identityclient.Account

func (a fakeAccounts) Account(_ context.Context, id string) (identityclient.Account, error) {
	acct, ok := a[id]
	if !ok {
		return identityclient.Account{}, apperror.Forbidden("No account")
	}
	return acct, nil
}

type fakeMailer struct {
	mails []usecase.InvitationMail
	fail  bool
}

func (m *fakeMailer) SendInvitation(_ context.Context, mail usecase.InvitationMail) error {
	if m.fail {
		return errors.New("notification down")
	}
	m.mails = append(m.mails, mail)
	return nil
}

type staffFixture struct {
	uc                         *usecase.StaffUseCase
	store                      *fakeStaffStore
	accounts                   fakeAccounts
	mailer                     *fakeMailer
	shop                       string
	owner, manager, clerk, out string
	now                        time.Time
}

func newStaffFixture(t *testing.T) *staffFixture {
	t.Helper()
	vendors := newFakeVendorRepository()
	f := &staffFixture{store: newFakeStaffStore(), mailer: &fakeMailer{}, owner: uuid.NewString(), manager: uuid.NewString(),
		clerk: uuid.NewString(), out: uuid.NewString(), now: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	f.shop = uuid.NewString()
	vendors.byID[f.shop] = &domain.Vendor{ID: f.shop, UserID: f.owner, ShopName: "Test shop", Status: domain.StatusApproved, Version: 1}
	f.accounts = fakeAccounts{
		f.owner:   {ID: f.owner, Email: "owner@example.test", Role: "vendor", Active: true},
		f.manager: {ID: f.manager, Email: "manager@example.test", Role: "buyer", Active: true},
		f.clerk:   {ID: f.clerk, Email: "clerk@example.test", Role: "buyer", Active: true},
		f.out:     {ID: f.out, Email: "outsider@example.test", Role: "vendor", Active: true},
	}
	f.store.members[key(f.shop, f.owner)] = &domain.Membership{VendorID: f.shop, UserID: f.owner, Role: domain.MemberOwner, Status: domain.MembershipActive, Version: 1}
	f.store.members[key(f.shop, f.manager)] = &domain.Membership{VendorID: f.shop, UserID: f.manager, Role: domain.MemberStaff, Status: domain.MembershipActive, Version: 1,
		Permissions: []string{shopaccess.OrdersFulfill, shopaccess.OrdersRead, shopaccess.StaffManage}}
	f.uc = &usecase.StaffUseCase{Staff: f.store, Vendors: vendors, Accounts: f.accounts, Tx: directTx{}, Mailer: f.mailer, Enabled: true,
		FingerprintKey: []byte("synthetic-test-fingerprint-key-000001"), AcceptURL: "https://shop.example.invalid/staff-invitations/accept",
		Now: func() time.Time { return f.now }, Log: zerolog.Nop()}
	return f
}

func code(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func (f *staffFixture) allowed(t *testing.T, actor, permission string) bool {
	t.Helper()
	res, err := f.uc.Authorize(t.Context(), actor, f.shop, permission)
	if err != nil {
		t.Fatalf("authorize %s: %v", permission, err)
	}
	return res.Allowed
}

// inviteAndDeliver returns the token the email carried.
func (f *staffFixture) inviteAndDeliver(t *testing.T, actor, email string, permissions ...string) string {
	t.Helper()
	if _, err := f.uc.Invite(t.Context(), actor, f.shop, usecase.InviteInput{Email: email, Permissions: permissions}); err != nil {
		t.Fatal(err)
	}
	if !f.uc.DeliverNextInvitation(t.Context()) {
		t.Fatal("no invitation delivered")
	}
	mail := f.mailer.mails[len(f.mailer.mails)-1]
	u, err := url.Parse(mail.URL)
	if err != nil || u.RawQuery != "" || !strings.HasPrefix(u.Fragment, "token=") {
		t.Fatalf("token must travel only in the link fragment: %q", mail.URL)
	}
	return strings.TrimPrefix(u.Fragment, "token=")
}

func TestAuthorizeOwnerStaffAndFeatureFlag(t *testing.T) {
	f := newStaffFixture(t)
	for _, p := range []string{shopaccess.ProductsWrite, shopaccess.PayoutDestinationWrite, shopaccess.FinanceRead} {
		if !f.allowed(t, f.owner, p) {
			t.Fatalf("owner refused %s", p)
		}
	}
	if !f.allowed(t, f.manager, shopaccess.OrdersFulfill) || f.allowed(t, f.manager, shopaccess.ProductsWrite) ||
		f.allowed(t, f.manager, shopaccess.PayoutDestinationWrite) {
		t.Fatal("staff must hold exactly its grants and never payout")
	}
	if f.allowed(t, f.out, shopaccess.OrdersRead) {
		t.Fatal("outsider allowed")
	}
	if _, err := f.uc.Authorize(t.Context(), f.owner, f.shop, "products.delete"); code(err) != apperror.CodeValidation {
		t.Fatalf("unknown permission must be refused as invalid, got %v", err)
	}
	f.uc.Enabled = false
	if f.allowed(t, f.manager, shopaccess.OrdersFulfill) || !f.allowed(t, f.owner, shopaccess.OrdersFulfill) {
		t.Fatal("feature off must keep owner-only parity")
	}
	f.uc.Enabled = true
	locked := f.accounts[f.manager]
	locked.Active = false
	f.accounts[f.manager] = locked
	if f.allowed(t, f.manager, shopaccess.OrdersFulfill) {
		t.Fatal("locked account allowed")
	}
	owner := f.accounts[f.owner]
	owner.Role = "buyer"
	f.accounts[f.owner] = owner
	if f.allowed(t, f.owner, shopaccess.OrdersRead) {
		t.Fatal("owner without a vendor account allowed")
	}
	shops, err := f.uc.AccessibleShops(t.Context(), f.owner)
	if err != nil || len(shops) != 0 {
		t.Fatalf("owner shops must need a vendor account: %v %d", err, len(shops))
	}
}

func TestInviteGrantsOnlyWhatTheActorHolds(t *testing.T) {
	f := newStaffFixture(t)
	ctx := t.Context()
	if _, err := f.uc.Invite(ctx, f.manager, f.shop, usecase.InviteInput{Email: "new@example.test", Permissions: []string{shopaccess.ProductsWrite}}); code(err) != shopaccess.CodePermissionDenied {
		t.Fatalf("staff manager granted a permission it lacks: %v", err)
	}
	if _, err := f.uc.Invite(ctx, f.owner, f.shop, usecase.InviteInput{Email: "new@example.test", Permissions: []string{shopaccess.PayoutDestinationWrite}}); code(err) != apperror.CodeValidation {
		t.Fatalf("owner-only permission granted to staff: %v", err)
	}
	if _, err := f.uc.Invite(ctx, f.owner, f.shop, usecase.InviteInput{Email: "new@example.test", Permissions: []string{shopaccess.MarketingManage}}); code(err) != apperror.CodeValidation {
		t.Fatalf("permission of a missing feature granted: %v", err)
	}
	if _, err := f.uc.Invite(ctx, f.out, f.shop, usecase.InviteInput{Email: "new@example.test", Permissions: []string{shopaccess.OrdersRead}}); code(err) != shopaccess.CodePermissionDenied {
		t.Fatalf("outsider invited: %v", err)
	}
	inv, err := f.uc.Invite(ctx, f.manager, f.shop, usecase.InviteInput{Email: " New@Example.test ", Permissions: []string{shopaccess.OrdersRead, shopaccess.OrdersRead}})
	if err != nil {
		t.Fatal(err)
	}
	if inv.EmailHint != "n***@example.test" || len(inv.Permissions) != 1 || len(inv.EmailFingerprint) != 64 {
		t.Fatalf("invitation stored wrongly: %+v", inv)
	}
	if !f.uc.DeliverNextInvitation(ctx) || len(f.mailer.mails) != 1 || f.mailer.mails[0].Email != "new@example.test" {
		t.Fatal("invitation email not sent")
	}
	f.uc.InvitesPaused = true
	if _, err := f.uc.Invite(ctx, f.owner, f.shop, usecase.InviteInput{Email: "x@example.test", Permissions: []string{shopaccess.OrdersRead}}); code(err) != "invitations_paused" {
		t.Fatalf("paused invitations accepted: %v", err)
	}
	f.uc.InvitesPaused, f.uc.Enabled = false, false
	if _, err := f.uc.Invite(ctx, f.owner, f.shop, usecase.InviteInput{Email: "x@example.test", Permissions: []string{shopaccess.OrdersRead}}); code(err) != "feature_disabled" {
		t.Fatalf("invitation with the feature off: %v", err)
	}
}

func TestAcceptInvitationOnceForTheInvitedAddress(t *testing.T) {
	f := newStaffFixture(t)
	ctx := t.Context()
	token := f.inviteAndDeliver(t, f.owner, "clerk@example.test", shopaccess.ProductsRead, shopaccess.InventoryAdjust)
	if _, err := f.uc.AcceptInvitation(ctx, f.out, token); code(err) != shopaccess.CodePermissionDenied {
		t.Fatalf("forwarded link accepted by another account: %v", err)
	}
	m, err := f.uc.AcceptInvitation(ctx, f.clerk, token)
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != domain.MemberStaff || !slices.Equal(m.Permissions, []string{shopaccess.InventoryAdjust, shopaccess.ProductsRead}) {
		t.Fatalf("unexpected membership %+v", m)
	}
	if f.accounts[f.clerk].Role != "buyer" {
		t.Fatal("joining must not change the global role")
	}
	if !f.allowed(t, f.clerk, shopaccess.InventoryAdjust) || f.allowed(t, f.clerk, shopaccess.ProductsWrite) {
		t.Fatal("buyer staff must act with exactly its grants")
	}
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, token); code(err) != "invitation_used" {
		t.Fatalf("replayed token: %v", err)
	}
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, "not-a-token"); code(err) != apperror.CodeNotFound {
		t.Fatalf("malformed token: %v", err)
	}
	last := f.store.audit[len(f.store.audit)-1]
	if last.Action != "staff_joined" || last.MembershipVersion == nil {
		t.Fatalf("join not audited: %+v", last)
	}
}

func TestAcceptRefusesExpiredSupersededAndOrphanInvitations(t *testing.T) {
	f := newStaffFixture(t)
	ctx := t.Context()
	first := f.inviteAndDeliver(t, f.owner, "clerk@example.test", shopaccess.ProductsRead)
	second := f.inviteAndDeliver(t, f.owner, "clerk@example.test", shopaccess.ProductsRead)
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, first); code(err) != "invitation_revoked" {
		t.Fatalf("superseded invitation accepted: %v", err)
	}
	f.now = f.now.Add(domain.InvitationTTL + time.Minute)
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, second); code(err) != "invitation_expired" {
		t.Fatalf("expired invitation accepted: %v", err)
	}
	f.now = f.now.Add(-domain.InvitationTTL - time.Minute)
	byManager := f.inviteAndDeliver(t, f.manager, "other@example.test", shopaccess.OrdersRead)
	other := uuid.NewString()
	f.accounts[other] = identityclient.Account{ID: other, Email: "other@example.test", Role: "buyer", Active: true}
	if err := f.uc.RemoveMember(ctx, f.owner, f.shop, f.manager, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.AcceptInvitation(ctx, other, byManager); code(err) != "invitation_revoked" {
		t.Fatalf("invitation of a revoked manager accepted: %v", err)
	}
}

func TestMemberChangesAreBoundedAndAudited(t *testing.T) {
	f := newStaffFixture(t)
	ctx := t.Context()
	token := f.inviteAndDeliver(t, f.owner, "clerk@example.test", shopaccess.OrdersRead, shopaccess.ProductsWrite)
	m, err := f.uc.AcceptInvitation(ctx, f.clerk, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateMember(ctx, f.manager, f.shop, f.manager, usecase.MemberChange{Permissions: []string{shopaccess.ProductsWrite}, ExpectedVersion: 1}); code(err) != shopaccess.CodePermissionDenied {
		t.Fatalf("self escalation: %v", err)
	}
	if _, err := f.uc.UpdateMember(ctx, f.manager, f.shop, f.clerk, usecase.MemberChange{Permissions: []string{shopaccess.OrdersRead}, ExpectedVersion: m.Version}); code(err) != shopaccess.CodePermissionDenied {
		t.Fatalf("manager touched a member holding more than it: %v", err)
	}
	if _, err := f.uc.UpdateMember(ctx, f.manager, f.shop, f.owner, usecase.MemberChange{Permissions: []string{shopaccess.OrdersRead}, ExpectedVersion: 1}); code(err) != "last_owner" {
		t.Fatalf("owner changed: %v", err)
	}
	if err := f.uc.RemoveMember(ctx, f.owner, f.shop, f.owner, 1, nil); code(err) != "last_owner" {
		t.Fatalf("owner removed: %v", err)
	}
	if _, err := f.uc.UpdateMember(ctx, f.owner, f.shop, f.clerk, usecase.MemberChange{Permissions: []string{shopaccess.OrdersRead}, ExpectedVersion: m.Version + 5}); code(err) != "version_conflict" {
		t.Fatalf("stale version accepted: %v", err)
	}
	reason := "Test narrowing"
	updated, err := f.uc.UpdateMember(ctx, f.owner, f.shop, f.clerk, usecase.MemberChange{Permissions: []string{shopaccess.OrdersRead}, ExpectedVersion: m.Version, Reason: &reason})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != m.Version+1 || f.allowed(t, f.clerk, shopaccess.ProductsWrite) {
		t.Fatal("narrowed permission still works")
	}
	last := f.store.audit[len(f.store.audit)-1]
	if last.Action != "staff_permissions_changed" || len(last.OldPermissions) != 2 || len(last.NewPermissions) != 1 {
		t.Fatalf("change not audited with old/new names: %+v", last)
	}
	// Revocation stops the very next request.
	if err := f.uc.RemoveMember(ctx, f.manager, f.shop, f.clerk, updated.Version, nil); err != nil {
		t.Fatal(err)
	}
	if f.allowed(t, f.clerk, shopaccess.OrdersRead) {
		t.Fatal("revoked member still allowed")
	}
	// With the feature off the owner can still take access back.
	f.uc.Enabled = false
	if err := f.uc.RemoveMember(ctx, f.owner, f.shop, f.manager, 1, nil); err != nil {
		t.Fatalf("owner could not remove staff with the feature off: %v", err)
	}
}

func TestInvitationDeliveryStoresOnlyTheTokenHash(t *testing.T) {
	f := newStaffFixture(t)
	f.mailer.fail = true
	if _, err := f.uc.Invite(t.Context(), f.owner, f.shop, usecase.InviteInput{Email: "clerk@example.test", Permissions: []string{shopaccess.OrdersRead}}); err != nil {
		t.Fatal(err)
	}
	if !f.uc.DeliverNextInvitation(t.Context()) || len(f.store.queued) != 1 {
		t.Fatal("failed delivery must stay queued")
	}
	f.mailer.fail = false
	if !f.uc.DeliverNextInvitation(t.Context()) {
		t.Fatal("retry not delivered")
	}
	u, _ := url.Parse(f.mailer.mails[0].URL)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(u.Fragment, "token="))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if len(f.store.tokens) != 1 || f.store.tokens[string(sum[:])] == "" {
		t.Fatal("only the hash of the last sent token may be stored")
	}
	if _, ok := f.store.emails[f.mailer.mails[0].DeliveryID]; ok {
		t.Fatal("address kept after delivery")
	}
}

// PW-022: with FEATURE_STAFF_REQUIRES_VERIFIED_EMAIL on, only an account
// that confirmed its email accepts; the link keeps working afterwards.
func TestAcceptInvitationNeedsAVerifiedEmail(t *testing.T) {
	f := newStaffFixture(t)
	ctx := t.Context()
	f.uc.RequireVerifiedEmail = true
	token := f.inviteAndDeliver(t, f.owner, "clerk@example.test", shopaccess.ProductsRead)
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, token); code(err) != "email_not_verified" {
		t.Fatalf("an unverified address cannot join: %v", err)
	}
	acct := f.accounts[f.clerk]
	acct.EmailVerified = true
	f.accounts[f.clerk] = acct
	if _, err := f.uc.AcceptInvitation(ctx, f.clerk, token); err != nil {
		t.Fatalf("a verified address joins with the same link: %v", err)
	}
}
