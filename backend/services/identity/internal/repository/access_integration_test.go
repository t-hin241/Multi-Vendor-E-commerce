package repository_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/usecase"
)

const testAdminPassword = "synthetic-test-password-1"

func (f *fixture) admin(t *testing.T) *domain.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{Email: "admin-" + uuid.NewString() + "@example.test", PasswordHash: string(hash), FullName: "Test admin", Role: domain.RoleAdmin}
	if err := f.users.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func errCode(err error) apperror.Code {
	var app *apperror.Error
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}

func TestIntegrationScopedPermissionsGrantRevokeAndBootstrap(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	manager, finance, other := f.admin(t), f.admin(t), f.admin(t)
	uc := f.access()

	// Flag off: every active admin keeps every bundle (behaviour before AF-19).
	if ok, _, err := uc.Check(ctx, finance.ID, adminaccess.FinanceApprove); err != nil || !ok {
		t.Fatalf("flag off must keep admin parity: %v", err)
	}
	uc.Scoped = true
	if ok, _, _ := uc.Check(ctx, finance.ID, adminaccess.FinanceApprove); ok {
		t.Fatal("scoped admin without a grant allowed")
	}
	if _, err := uc.Grant(ctx, manager.ID, finance.ID, adminaccess.FinanceApprove, "Test grant", 0); errCode(err) != adminaccess.CodeMissingPermission {
		t.Fatalf("grant without access.manage: %v", err)
	}
	if err := uc.BootstrapAccessManager(ctx, manager.Email, "test-operator", "Test bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := uc.BootstrapAccessManager(ctx, other.Email, "test-operator", "Test bootstrap"); err == nil {
		t.Fatal("second bootstrap accepted")
	}
	if _, err := uc.Grant(ctx, manager.ID, manager.ID, adminaccess.FinanceApprove, "Test self grant", 1); errCode(err) != "self_approval" {
		t.Fatalf("self grant: %v", err)
	}
	if _, err := uc.Grant(ctx, manager.ID, finance.ID, adminaccess.FinanceApprove, "Test grant", 5); errCode(err) != "stale_snapshot" {
		t.Fatalf("stale version accepted: %v", err)
	}
	g, err := uc.Grant(ctx, manager.ID, finance.ID, adminaccess.FinanceApprove, "Test grant", 0)
	if err != nil {
		t.Fatal(err)
	}
	ok, version, err := uc.Check(ctx, finance.ID, adminaccess.FinanceApprove)
	if err != nil || !ok || version != 1 {
		t.Fatalf("granted bundle not effective: %v %v %d", ok, err, version)
	}
	if ok, _, _ := uc.Check(ctx, finance.ID, adminaccess.AccessManage); ok {
		t.Fatal("one bundle granted another")
	}
	if _, err := uc.Grant(ctx, manager.ID, finance.ID, adminaccess.FinanceApprove, "Test again", 1); errCode(err) != "already_granted" {
		t.Fatalf("duplicate grant: %v", err)
	}
	// Two managers changing the same version: one wins.
	results := make(chan error, 2)
	for _, b := range []string{adminaccess.FinanceRead, adminaccess.AuditRead} {
		go func() {
			_, err := uc.Grant(context.WithoutCancel(ctx), manager.ID, finance.ID, b, "Test race", 1)
			results <- err
		}()
	}
	won := 0
	for range 2 {
		if <-results == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("expected one winner of the version race, got %d", won)
	}
	if err := uc.Revoke(ctx, manager.ID, g.ID, "Test revoke", 2); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := uc.Check(ctx, finance.ID, adminaccess.FinanceApprove); ok {
		t.Fatal("revoked bundle still effective")
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM identity_audit_logs WHERE action IN ('permission_granted','permission_revoked','access_bootstrap')`).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("grant changes not audited: %v %d", err, audits)
	}
	// A deactivated admin holds nothing, whatever its grants say.
	if _, err := f.pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, manager.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := uc.Check(ctx, manager.ID, adminaccess.AccessManage); ok {
		t.Fatal("inactive admin kept its bundle")
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000005_admin_permissions.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "keep migration 000005") {
		t.Fatalf("down migration dropped grant history: %v", err)
	}
}

func TestIntegrationReauthProofIsOneTimeAndBound(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	a, b := f.admin(t), f.admin(t)
	now := time.Now().UTC()
	uc := f.access()
	uc.Now = func() time.Time { return now }
	if _, err := uc.Reauthenticate(ctx, a.ID, "wrong-password", "payment.approval.decide", "hash-1"); errCode(err) != apperror.CodeUnauthorized {
		t.Fatalf("wrong password accepted: %v", err)
	}
	proof, err := uc.Reauthenticate(ctx, a.ID, testAdminPassword, "payment.approval.decide", "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ user, purpose, op string }{
		{b.ID, "payment.approval.decide", "hash-1"},
		{a.ID, "payment.approval.submit", "hash-1"},
		{a.ID, "payment.approval.decide", "hash-2"},
	} {
		if err := uc.ConsumeProof(ctx, proof.Value, tc.user, tc.purpose, tc.op); errCode(err) != adminaccess.CodeReauthRequired {
			t.Fatalf("proof accepted for another user/purpose/operation %+v: %v", tc, err)
		}
	}
	if err := uc.ConsumeProof(ctx, proof.Value, a.ID, "payment.approval.decide", "hash-1"); err != nil {
		t.Fatal(err)
	}
	if err := uc.ConsumeProof(ctx, proof.Value, a.ID, "payment.approval.decide", "hash-1"); errCode(err) != adminaccess.CodeReauthRequired {
		t.Fatal("proof replayed")
	}
	late, err := uc.Reauthenticate(ctx, a.ID, testAdminPassword, "payment.approval.decide", "hash-3")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(domain.ProofTTL + time.Second)
	if err := uc.ConsumeProof(ctx, late.Value, a.ID, "payment.approval.decide", "hash-3"); errCode(err) != adminaccess.CodeReauthRequired {
		t.Fatal("expired proof accepted")
	}
	var stored int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM reauth_proofs WHERE proof_hash = convert_to($1, 'UTF8')`, proof.Value).Scan(&stored); err != nil || stored != 0 {
		t.Fatal("raw proof stored")
	}
	buyer := &domain.User{Email: "buyer-" + uuid.NewString() + "@example.test", PasswordHash: "x", FullName: "Test buyer", Role: domain.RoleBuyer}
	if err := f.users.Create(ctx, buyer); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Reauthenticate(ctx, buyer.ID, testAdminPassword, "payment.approval.decide", "hash-1"); errCode(err) != apperror.CodeForbidden {
		t.Fatalf("non-admin got a proof: %v", err)
	}
}

// PW-028: with the second factor required, reauthentication needs a code
// from the admin's enrolled authenticator (each code once) or an unused
// recovery code; an admin without an authenticator must set one up first.
func TestIntegrationReauthNeedsTheAuthenticatorCode(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	a, b := f.admin(t), f.admin(t)
	cipher, err := usecase.NewTokenCipher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	totp := &usecase.TOTPUseCase{Store: repository.TOTPRepository{Pool: f.pool}, Users: f.users, Tx: f.tx, Cipher: cipher, Issuer: "Test",
		Now: func() time.Time { return now }, Log: zerolog.Nop()}
	enrollment, err := totp.Start(ctx, a.ID)
	if err != nil || !strings.HasPrefix(enrollment.URI, "otpauth://totp/") {
		t.Fatalf("start: %+v %v", enrollment, err)
	}
	secret, err := domain.TOTPSecretBytes(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := totp.Confirm(ctx, a.ID, "000000"); errCode(err) != "mfa_code_invalid" && err == nil {
		t.Fatal("a wrong code does not confirm")
	}
	codes, err := totp.Confirm(ctx, a.ID, domain.TOTPCode(secret, domain.TOTPStepAt(now)))
	if err != nil || len(codes) != domain.RecoveryCodeCount {
		t.Fatalf("confirm: %v %v", codes, err)
	}
	var stored int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM admin_totp WHERE position(convert_to($1, 'UTF8') in secret_ciphertext) > 0`, enrollment.Secret).Scan(&stored); err != nil || stored != 0 {
		t.Fatal("the secret is stored encrypted only")
	}

	uc := f.access()
	uc.Now = func() time.Time { return now }
	uc.SecondFactor, uc.MFARequired = totp, true
	reauth := func(user, code string) error {
		_, err := uc.Reauthenticate(ctx, user, testAdminPassword, "payment.approval.decide", "hash-mfa", code)
		return err
	}
	if err := reauth(a.ID, ""); errCode(err) != "mfa_code_required" {
		t.Fatalf("no code: %v", err)
	}
	if err := reauth(a.ID, domain.TOTPCode(secret, domain.TOTPStepAt(now))); errCode(err) != "mfa_code_invalid" {
		t.Fatalf("the enrollment code is not reused: %v", err)
	}
	now = now.Add(domain.TOTPStep)
	next := domain.TOTPCode(secret, domain.TOTPStepAt(now))
	if err := reauth(a.ID, next); err != nil {
		t.Fatal(err)
	}
	if err := reauth(a.ID, next); errCode(err) != "mfa_code_invalid" {
		t.Fatalf("a code works once: %v", err)
	}
	if err := reauth(a.ID, codes[0]); err != nil {
		t.Fatalf("a recovery code works: %v", err)
	}
	if err := reauth(a.ID, codes[0]); errCode(err) != "mfa_code_invalid" {
		t.Fatalf("a recovery code works once: %v", err)
	}
	if err := reauth(b.ID, "123456"); errCode(err) != "mfa_enrollment_required" {
		t.Fatalf("an admin without an authenticator sets one up first: %v", err)
	}
	if _, err := totp.Start(ctx, a.ID); errCode(err) != "mfa_already_enrolled" {
		t.Fatalf("an enrolled authenticator is not replaced silently: %v", err)
	}
}
