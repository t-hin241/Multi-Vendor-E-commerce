package repository_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type capturedMail struct {
	mu    sync.Mutex
	mails []usecase.InvitationMail
}

func (c *capturedMail) SendInvitation(_ context.Context, m usecase.InvitationMail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mails = append(c.mails, m)
	return nil
}

func (c *capturedMail) token(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	u, err := url.Parse(c.mails[len(c.mails)-1].URL)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(u.Fragment, "token=")
}

func staffUseCase(f *fixture, mail *capturedMail) *usecase.StaffUseCase {
	return &usecase.StaffUseCase{Staff: repository.StaffRepository{Pool: f.db}, Vendors: f.vendors, Accounts: f.ops.Actors.(actors), Tx: f.ops.Tx,
		Mailer: mail, Enabled: true, FingerprintKey: []byte("synthetic-test-fingerprint-key-000001"),
		AcceptURL: "https://shop.example.invalid/staff-invitations/accept", Log: zerolog.Nop()}
}

func TestIntegrationOwnerMembershipFollowsTheShop(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	var role, status string
	if err := f.db.QueryRow(ctx, `SELECT role, status FROM vendor_memberships WHERE vendor_id=$1 AND user_id=$2`, v.ID, f.owner).Scan(&role, &status); err != nil {
		t.Fatal("new shop has no owner membership:", err)
	}
	if role != "owner" || status != "active" {
		t.Fatalf("unexpected owner membership %s/%s", role, status)
	}
	if _, err := f.db.Exec(ctx, `UPDATE vendors SET user_id=$2 WHERE id=$1`, v.ID, f.other); err == nil {
		t.Fatal("shop ownership changed")
	}
	if _, err := f.db.Exec(ctx, `UPDATE vendor_memberships SET status='revoked', revoked_at=now() WHERE vendor_id=$1 AND role='owner'`, v.ID); err == nil {
		t.Fatal("owner membership revoked")
	}
}

func TestIntegrationStaffInviteAcceptAuthorizeRevoke(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	clerk := uuid.NewString()
	f.ops.Actors.(actors)[clerk] = "buyer"
	mail := &capturedMail{}
	uc := staffUseCase(f, mail)
	if _, err := uc.Invite(ctx, f.owner, v.ID, usecase.InviteInput{Email: clerk + "@example.test", Permissions: []string{shopaccess.OrdersFulfill, shopaccess.OrdersRead}}); err != nil {
		t.Fatal(err)
	}
	if !uc.DeliverNextInvitation(ctx) || len(mail.mails) != 1 {
		t.Fatal("invitation not delivered")
	}
	var stored int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM staff_invitations WHERE vendor_id=$1 AND delivery_email IS NULL AND delivery_status='sent'
		AND token_hash IS NOT NULL AND email_hint LIKE '%***@example.test'`, v.ID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("address kept or token hash missing after delivery: %v %d", err, stored)
	}
	token := mail.token(t)

	// Two accepts of the same link: exactly one joins.
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() { <-start; _, err := uc.AcceptInvitation(ctx, clerk, token); results <- err }()
	}
	close(start)
	joined := 0
	for range 2 {
		if err := <-results; err == nil {
			joined++
		}
	}
	if joined != 1 {
		t.Fatalf("expected one join, got %d", joined)
	}

	gin.SetMode(gin.ReleaseMode)
	log := zerolog.Nop()
	router := transport.NewRouter("test", log, authjwttest.Manager(), transport.NewVendorHandler(f.uc, log), transport.NewVendorAddressHandler(f.addresses, log),
		transport.NewAdminHandler(f.uc, log), transport.NewInternalHandler(f.uc, log), transport.NewPolicyHandler(&usecase.PolicyUseCase{}, f.uc, log),
		transport.NewStaffHandler(uc, log), adminaccesstest.Guard(transport.AdminRoutes), serviceauth.SharedKey("test-internal-service-key"))
	authorize := func(actor, permission string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"actor_user_id": actor, "vendor_id": v.ID, "permission": permission})
		req := httptest.NewRequest("POST", "/internal/vendors/authorize", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(serviceauth.Header, "test-internal-service-key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("authorize answered %d: %s", w.Code, w.Body.String())
		}
		var out struct{ Data map[string]any }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	if got := authorize(clerk, shopaccess.OrdersFulfill); got["allowed"] != true || got["role"] != "staff" {
		t.Fatalf("staff refused its grant: %v", got)
	}
	for _, p := range []string{shopaccess.ProductsWrite, shopaccess.PayoutDestinationWrite} {
		if got := authorize(clerk, p); got["allowed"] != false {
			t.Fatalf("staff allowed %s", p)
		}
	}
	if got := authorize(f.owner, shopaccess.PayoutDestinationWrite); got["allowed"] != true || got["role"] != "owner" {
		t.Fatalf("owner refused: %v", got)
	}
	if got := authorize(f.other, shopaccess.OrdersRead); got["allowed"] != false {
		t.Fatal("another vendor allowed")
	}

	m, err := uc.GetMember(ctx, f.owner, v.ID, clerk)
	if err != nil {
		t.Fatal(err)
	}
	if err := uc.RemoveMember(ctx, f.owner, v.ID, clerk, m.Version, nil); err != nil {
		t.Fatal(err)
	}
	if got := authorize(clerk, shopaccess.OrdersRead); got["allowed"] != false {
		t.Fatal("revoked member still allowed on the next request")
	}
	var actions []string
	rows, err := f.db.Query(ctx, `SELECT action FROM membership_audit_logs WHERE vendor_id=$1 ORDER BY created_at, action`, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
	}
	if strings.Join(actions, ",") != "staff_invited,staff_joined,staff_revoked" {
		t.Fatalf("unexpected audit trail %v", actions)
	}
	if _, err := f.db.Exec(ctx, `DELETE FROM membership_audit_logs WHERE vendor_id=$1`, v.ID); err == nil {
		t.Fatal("membership audit is not append-only")
	}
	var leaked int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM membership_audit_logs WHERE array_to_string(new_permissions || old_permissions, ',') LIKE '%@%'
		OR coalesce(reason,'') LIKE '%@%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("audit carries an address")
	}
}

func TestIntegrationStaffMigrationRefusesToDropHistory(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	uc := staffUseCase(f, &capturedMail{})
	uc.Now = func() time.Time { return time.Now().UTC() }
	if _, err := uc.Invite(ctx, f.owner, v.ID, usecase.InviteInput{Email: "someone@example.test", Permissions: []string{shopaccess.OrdersRead}}); err != nil {
		t.Fatal(err)
	}
	down := readMigration(t, "000011_shop_staff.down.sql")
	if _, err := f.db.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "keep migration 000011") {
		t.Fatalf("down migration dropped staff history: %v", err)
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join("../../migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(sql)
}
