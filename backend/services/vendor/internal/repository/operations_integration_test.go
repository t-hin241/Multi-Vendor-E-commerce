package repository_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/vendorsvc/internal/adapter"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

func testDB(t *testing.T, migrations string) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("VENDOR_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("VENDOR_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("integration database name must end with _test")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "vendor_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	files, err := filepath.Glob(filepath.Join(migrations, "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(file), err)
		}
	}
	return pool
}

type actors map[string]string

func (a actors) RequireRole(ctx context.Context, id, role string) error {
	if a[id] != role {
		return apperror.Forbidden("Wrong role")
	}
	return nil
}

type fixture struct {
	db                  *pgxpool.Pool
	vendors             *repository.VendorRepository
	audit               *repository.AuditLogRepository
	ops                 usecase.Operations
	uc                  *usecase.VendorUseCase
	addresses           *usecase.VendorAddressUseCase
	owner, other, admin string
}

func setup(t *testing.T) *fixture {
	db := testDB(t, "../../migrations")
	v := repository.NewVendorRepository(db)
	a := repository.NewAuditLogRepository(db)
	addresses := repository.NewVendorAddressRepository(db)
	f := &fixture{db: db, vendors: v, audit: a, owner: uuid.NewString(), other: uuid.NewString(), admin: uuid.NewString()}
	f.ops = usecase.Operations{Tx: repository.Transactions{Pool: db}, Actors: actors{f.owner: "vendor", f.other: "vendor", f.admin: "admin"}, Addresses: addresses, Events: repository.Outbox{Pool: db}, Notices: repository.NotificationOutbox{Pool: db}}
	f.uc = usecase.NewVendorUseCase(v, a, nil, zerolog.Nop(), f.ops)
	f.addresses = usecase.NewVendorAddressUseCase(addresses, v, f.ops)
	return f
}
func (f *fixture) shop(t *testing.T) *domain.Vendor {
	t.Helper()
	v, err := f.uc.Apply(t.Context(), f.owner, "Test shop", "Test description")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.addresses.Add(t.Context(), f.owner, v.ID, "Test owner", "0900000000", "Test province", "Test district", "Test ward", "Test street"); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestIntegrationDecisionAtomicityAndRace(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := f.uc.Approve(ctx, v.ID, f.admin); results <- err }()
	go func() { <-start; _, err := f.uc.Reject(ctx, v.ID, f.admin, "Test rejection"); results <- err }()
	close(start)
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected one successful decision, got %d", success)
	}
	var audits, events int
	if err := f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM vendor_audit_logs WHERE vendor_id=$1),(SELECT count(*) FROM vendor_outbox WHERE vendor_id=$1)`, v.ID).Scan(&audits, &events); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || events != 2 {
		t.Fatalf("audit=%d events=%d", audits, events)
	}
	other := f.shop(t)
	ops := f.ops
	ops.Events = failEvents{}
	broken := usecase.NewVendorUseCase(f.vendors, f.audit, nil, zerolog.Nop(), ops)
	if _, err := broken.Approve(ctx, other.ID, f.admin); err == nil {
		t.Fatal("expected outbox error")
	}
	saved, err := f.vendors.FindByID(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.StatusPending || saved.Version != 1 {
		t.Fatal("decision survived outbox rollback")
	}
	audit, err := f.audit.List(ctx, other.ID, 20, 0)
	if err != nil || len(audit) != 0 {
		t.Fatal("audit survived outbox rollback", err)
	}
	if _, err = f.uc.Approve(ctx, other.ID, f.owner); err == nil {
		t.Fatal("vendor could approve")
	}
}

type failEvents struct{}

func TestIntegrationOutboxAcknowledgementAndCatalogMigration(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	outbox := repository.Outbox{Pool: f.db}
	batch, err := outbox.Claim(ctx)
	if err != nil || len(batch) != 1 {
		t.Fatal("could not claim application event", err)
	}
	if err = outbox.Complete(ctx, batch[0], false); err != nil {
		t.Fatal(err)
	}
	saved, err := f.vendors.FindByID(ctx, v.ID)
	if err != nil || saved.EnforcedVersion != 0 {
		t.Fatal("failed delivery acknowledged", err)
	}
	if err = outbox.Complete(ctx, batch[0], true); err != nil {
		t.Fatal(err)
	}
	saved, err = f.vendors.FindByID(ctx, v.ID)
	if err != nil || saved.EnforcedVersion != saved.Version {
		t.Fatal("successful delivery not acknowledged", err)
	}
	summary, err := f.uc.Operations(ctx, f.admin)
	if err != nil || summary.PendingStatusEvents != 0 || summary.PendingApplications != 1 {
		t.Fatal("incorrect operations summary", err)
	}
	catalog := testDB(t, "../../../catalog/migrations")
	store := vendorsales.Store{Pool: catalog}
	if err = store.Apply(ctx, vendorsales.Status{VendorID: v.ID, Status: "suspended", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if err = store.Apply(ctx, vendorsales.Status{VendorID: v.ID, Status: "approved", Version: 1}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = catalog.QueryRow(ctx, `SELECT status FROM vendor_sale_status WHERE vendor_id=$1`, v.ID).Scan(&status); err != nil || status != "suspended" {
		t.Fatal("old event restored selling permission", err)
	}
}

func (failEvents) Queue(context.Context, *domain.Vendor) error {
	return errors.New("test outbox failure")
}
func (failEvents) Replay(context.Context, string) error { return nil }
func TestIntegrationReadinessOwnershipAndDefaultRace(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	v, err := f.uc.Apply(ctx, f.owner, "Test shop", "Test description")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.Approve(ctx, v.ID, f.admin); err == nil {
		t.Fatal("approved without pickup address")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.addresses.Add(ctx, f.owner, v.ID, "Test owner", "0900000000", "P", "D", "W", "Street")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.addresses.ListMine(ctx, f.owner, v.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	var defaultID string
	for _, a := range list {
		if a.IsDefault {
			defaults++
			defaultID = a.ID
		}
	}
	if defaults != 1 {
		t.Fatalf("got %d defaults", defaults)
	}
	if err = f.addresses.Delete(ctx, f.owner, v.ID, defaultID); err == nil {
		t.Fatal("deleted default pickup")
	}
	sameOwner := f.shop(t)
	if err = f.addresses.SetDefault(ctx, f.owner, sameOwner.ID, defaultID); err == nil {
		t.Fatal("cross-shop address accepted")
	}
	if _, err = f.uc.UpdateProfile(ctx, f.other, v.ID, "Stolen", "description", ""); err == nil {
		t.Fatal("cross-owner profile accepted")
	}
	if _, err = f.uc.Reject(ctx, v.ID, f.admin, "Missing evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.Resubmit(ctx, f.other, v.ID); err == nil {
		t.Fatal("cross-owner resubmission")
	}
	if _, err = f.uc.Resubmit(ctx, f.owner, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.Approve(ctx, v.ID, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.Suspend(ctx, v.ID, f.admin, "Test suspension"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.Resubmit(ctx, f.owner, v.ID); err == nil {
		t.Fatal("owner bypassed suspension")
	}
	if _, err = f.uc.GetOwned(ctx, f.owner, v.ID); err != nil {
		t.Fatal("suspension removed ownership access")
	}
	if _, err = f.uc.Restore(ctx, v.ID, f.admin, "Review complete"); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationPayoutVersionsAndAudit(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	secondShop := f.shop(t)
	ctx := t.Context()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := adapter.NewPayoutCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.PayoutRepository{Pool: f.db}
	uc := &usecase.PayoutUseCase{Accounts: repo, Vendors: f.vendors, Audit: f.audit, Ops: f.ops, Cipher: cipher}
	a, err := uc.Submit(ctx, f.owner, v.ID, "000000", "0000001234", "TEST ACCOUNT OWNER")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(a.NumberCipher, []byte("0000001234")) {
		t.Fatal("plaintext persisted")
	}
	if _, err = uc.List(ctx, f.other, v.ID, false, 20, 0); err == nil {
		t.Fatal("cross-owner payout read")
	}
	if _, err = uc.Decide(ctx, f.owner, v.ID, a.ID, a.Version, true, "Test evidence"); err == nil {
		t.Fatal("owner verified account")
	}
	if _, err = uc.Decide(ctx, f.admin, secondShop.ID, a.ID, a.Version, true, "Test evidence"); err == nil {
		t.Fatal("cross-shop account verification")
	}
	if _, err = uc.Details(ctx, "", v.ID, a.ID, a.Version, "test batch", true); err == nil {
		t.Fatal("pending account released to payment")
	}
	if _, err = uc.Decide(ctx, f.admin, v.ID, a.ID, a.Version, true, "Test evidence"); err != nil {
		t.Fatal(err)
	}
	b, err := uc.Submit(ctx, f.owner, v.ID, "000000", "0000005678", "TEST REPLACEMENT")
	if err != nil {
		t.Fatal(err)
	}
	old, err := repo.Find(ctx, v.ID, a.ID, a.Version)
	if err != nil || !old.Default {
		t.Fatal("pending change replaced verified default", err)
	}
	if _, err = uc.Decide(ctx, f.admin, v.ID, b.ID, b.Version, true, "Test evidence"); err != nil {
		t.Fatal(err)
	}
	details, err := uc.Details(ctx, "", v.ID, a.ID, a.Version, "test batch original destination", true)
	if err != nil {
		t.Fatal(err)
	}
	if details.Number != "0000001234" {
		t.Fatal("old batch redirected")
	}
	var reads int
	if err = f.db.QueryRow(ctx, `SELECT count(*) FROM vendor_payout_details_audit WHERE account_id=$1`, a.ID).Scan(&reads); err != nil || reads != 1 {
		t.Fatal("missing details audit", err)
	}
	if _, err = f.db.Exec(ctx, `UPDATE vendor_payout_accounts SET bank_bin='000001' WHERE id=$1`, a.ID); err == nil {
		t.Fatal("immutable destination updated")
	}
	if _, err = uc.Details(ctx, f.owner, v.ID, a.ID, a.Version, "curiosity", false); err == nil {
		t.Fatal("owner bypassed detail scope")
	}
}
func TestIntegrationSuspensionCheckoutFence(t *testing.T) {
	db := testDB(t, "../../../order/migrations")
	ctx := t.Context()
	id := uuid.NewString()
	store := vendorsales.Store{Pool: db}
	approved := vendorsales.Status{VendorID: id, Status: "approved", Version: 1}
	if err := store.Apply(ctx, approved); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = vendorsales.LockApproved(ctx, tx, map[string]int64{id: 1}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.Apply(ctx, vendorsales.Status{VendorID: id, Status: "suspended", Version: 2}) }()
	select {
	case err := <-done:
		t.Fatalf("suspension acknowledged before prior checkout completed: %v", err)
	case <-time.After(120 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = store.Apply(ctx, approved); err != nil {
		t.Fatal(err)
	}
	next, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Rollback(ctx)
	if err = vendorsales.LockApproved(ctx, next, map[string]int64{id: 1}); err == nil {
		t.Fatal("checkout accepted after confirmed suspension")
	}
}
func TestIntegrationPayoutBatchDestinationIsPinned(t *testing.T) {
	db := testDB(t, "../../../payment/migrations")
	ctx := t.Context()
	var batch, id string
	if err := db.QueryRow(ctx, `INSERT INTO payout_batches(provider,idempotency_key,status,created_by) VALUES('test',$1,'pending',$2) RETURNING id`, uuid.NewString(), uuid.NewString()).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO payout_items(payout_batch_id,vendor_id,vendor_order_id,amount,destination_mask,status,destination_account_id,destination_version,currency) VALUES($1,$2,$3,100,'****1234','pending',$4,1,'VND') RETURNING id`, batch, uuid.NewString(), uuid.NewString(), uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE payout_items SET destination_account_id=$2 WHERE id=$1`, id, uuid.NewString()); err == nil {
		t.Fatal("pending payout redirected")
	}
	if _, err := db.Exec(ctx, `INSERT INTO payout_items(payout_batch_id,vendor_id,vendor_order_id,amount,destination_mask,status,destination_account_id,currency) VALUES($1,$2,$3,100,'****1234','pending',$4,'VND')`, batch, uuid.NewString(), uuid.NewString(), uuid.NewString()); err == nil {
		t.Fatal("missing destination version accepted")
	}
	if _, err := db.Exec(ctx, `UPDATE payout_items SET status='succeeded', evidence_reference='FAKE-TRANSFER-REF' WHERE id=$1`, id); err != nil {
		t.Fatal("ordinary payment state update failed", err)
	}
}

func TestIntegrationAPIAuthAndPayoutScopes(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	manager := authjwttest.Manager()
	gin.SetMode(gin.ReleaseMode)
	log := zerolog.Nop()
	router := transport.NewRouter("test", log, manager, transport.NewVendorHandler(f.uc, log), transport.NewVendorAddressHandler(f.addresses, log), transport.NewAdminHandler(f.uc, log), transport.NewInternalHandler(f.uc, log), transport.NewPolicyHandler(&usecase.PolicyUseCase{}, f.uc, log), serviceauth.SharedKey("test-internal-service-key"))
	cipher, err := adapter.NewPayoutCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	uc := &usecase.PayoutUseCase{Accounts: repository.PayoutRepository{Pool: f.db}, Vendors: f.vendors, Audit: f.audit, Ops: f.ops, Cipher: cipher}
	(transport.PayoutHandler{UseCase: uc, Log: log}).Register(router, middleware.RequireAuth(manager), "test-payment-scope-key", serviceauth.SharedKey("test-internal-service-key"))
	ownerToken, _, err := manager.IssueAccessToken(f.owner, "vendor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := manager.IssueAccessToken(f.other, "vendor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := manager.IssueAccessToken(f.admin, "admin", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, err := uc.Submit(ctx, f.owner, v.ID, "000000", "0000001234", "TEST ACCOUNT OWNER")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path, token, header, value, body string
		want                                           int
	}{
		{name: "anonymous owner", method: "GET", path: "/api/vendor/" + v.ID, want: 401},
		{name: "other owner", method: "GET", path: "/api/vendor/" + v.ID, token: otherToken, want: 403},
		{name: "non-admin decision", method: "PATCH", path: "/api/vendor/admin/applications/" + v.ID + "/approve", token: ownerToken, want: 403},
		{name: "internal missing key", method: "GET", path: "/internal/vendors/sale-status", want: 403},
		{name: "internal lookup", method: "GET", path: "/internal/vendors/sale-status", header: "X-Identity-Service-Key", value: "test-internal-service-key", want: 200},
		{name: "wrong payout scope", method: "POST", path: "/internal/payout-destinations/" + v.ID + "/" + a.ID, header: "X-Identity-Service-Key", value: "test-internal-service-key", body: `{"version":1,"purpose":"test payout"}`, want: 403},
		{name: "pending payout denied", method: "POST", path: "/internal/payout-destinations/" + v.ID + "/" + a.ID, header: "X-Vendor-Payout-Key", value: "test-payment-scope-key", body: `{"version":1,"purpose":"test payout"}`, want: 409},
		{name: "other owner payout list", method: "GET", path: "/api/vendor/" + v.ID + "/payout-accounts", token: otherToken, want: 403},
		{name: "masked owner payout list", method: "GET", path: "/api/vendor/" + v.ID + "/payout-accounts", token: ownerToken, want: 200},
		{name: "masked admin payout list", method: "GET", path: "/api/vendor/admin/shops/" + v.ID + "/payout-accounts", token: adminToken, want: 200},
		{name: "default destination needs key", method: "GET", path: "/internal/payout-destinations/" + v.ID + "/default", want: 403},
		{name: "no verified destination yet", method: "GET", path: "/internal/payout-destinations/" + v.ID + "/default", header: "X-Identity-Service-Key", value: "test-internal-service-key", want: 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "0000001234") || strings.Contains(w.Body.String(), "TEST ACCOUNT OWNER") {
				t.Fatal("plaintext leaked in ordinary response")
			}
		})
	}
	if _, err := uc.Decide(ctx, f.admin, v.ID, a.ID, a.Version, true, "Test evidence"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/internal/payout-destinations/"+v.ID+"/default", nil)
	req.Header.Set("X-Identity-Service-Key", "test-internal-service-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"last4":"1234"`) || !strings.Contains(body, a.ID) || strings.Contains(body, "0000001234") || strings.Contains(body, "TEST ACCOUNT OWNER") {
		t.Fatalf("expected the masked verified destination, got %d %s", w.Code, body)
	}
}

// ADM-05: a status replay needs an admin and a reason, and is audited.
func TestIntegrationReplayIsAuditedWithReason(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	if err := f.uc.Replay(ctx, f.admin, v.ID, " "); err == nil {
		t.Fatal("a replay needs a reason")
	}
	if err := f.uc.Replay(ctx, f.owner, v.ID, "Catalog was down"); err == nil {
		t.Fatal("a vendor cannot replay")
	}
	if err := f.uc.Replay(ctx, f.admin, v.ID, "Catalog was down"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vendor_audit_logs WHERE vendor_id=$1 AND action='event_replayed' AND actor_user_id=$2`, v.ID, f.admin).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expected one replay audit row, got %d %v", n, err)
	}
	if _, err := f.db.Exec(ctx, `DELETE FROM vendor_audit_logs WHERE vendor_id=$1`, v.ID); err == nil {
		t.Fatal("audit rows must not be deletable")
	}
}

// NTF-01: the owner's notice commits with the decision and waits in the
// outbox until Notification accepts it; a refusal parks it.
func TestIntegrationDecisionNoticeIsQueuedWithTheDecision(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	v := f.shop(t)
	if _, err := f.uc.Approve(ctx, v.ID, f.admin); err != nil {
		t.Fatal(err)
	}
	var notices int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vendor_notification_outbox WHERE vendor_id=$1 AND type='vendor_approved' AND user_id=$2`, v.ID, f.owner).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("expected one queued notice, got %d %v", notices, err)
	}
	outbox := repository.NotificationOutbox{Pool: f.db}
	batch, err := outbox.Claim(ctx)
	if err != nil || len(batch) != 1 {
		t.Fatalf("claim: %v %v", batch, err)
	}
	if again, _ := outbox.Claim(ctx); len(again) != 0 {
		t.Fatal("a leased notice must not be claimed twice")
	}
	if err := outbox.Failed(ctx, batch[0], "notification refused with Bad Request", true); err != nil {
		t.Fatal(err)
	}
	summary, err := f.vendors.Operations(ctx)
	if err != nil || summary.ParkedNotices != 1 || summary.PendingNotices != 0 {
		t.Fatalf("parked notice must be visible: %+v %v", summary, err)
	}

	// A decision that fails to queue its notice is rolled back.
	other := f.shop(t)
	ops := f.ops
	ops.Notices = nil
	broken := usecase.NewVendorUseCase(f.vendors, f.audit, nil, zerolog.Nop(), ops)
	if _, err := broken.Reject(ctx, other.ID, f.admin, "Test rejection"); err == nil {
		t.Fatal("expected the decision to fail without its notice")
	}
	if current, _ := f.vendors.FindByID(ctx, other.ID); current.Status != domain.StatusPending {
		t.Fatalf("the decision must roll back, shop is %s", current.Status)
	}
}
