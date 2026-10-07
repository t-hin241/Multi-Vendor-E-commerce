package repository_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/services/identity/internal/adapter"
	"shopee/backend/services/identity/internal/config"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/transport"
	"shopee/backend/services/identity/internal/usecase"
)

type fixture struct {
	pool    *pgxpool.Pool
	auth    *usecase.AuthUseCase
	users   *repository.UserRepository
	resets  *repository.PasswordResetRepository
	refresh *repository.RefreshTokenRepository
	tx      repository.Transactions
	cipher  *usecase.TokenCipher
	jwt     *authjwt.Manager
}

func (f *fixture) access() *usecase.AccessUseCase {
	return &usecase.AccessUseCase{Store: repository.AccessRepository{Pool: f.pool}, Users: f.users, Tx: f.tx}
}
func (f *fixture) accessHandler() *transport.AccessHandler {
	return transport.NewAccessHandler(f.access(), zerolog.Nop())
}
func (f *fixture) accessGuard() gin.HandlerFunc {
	return adminaccess.Guard(f.access(), transport.AdminRoutes, zerolog.Nop())
}

func TestIntegrationResetDeliveryRetryWipeAndExpiry(t *testing.T) {
	f := setup(t)
	f.register(t)
	// Simulate an ID-only notification job that fetches material from Identity.
	key := strings.Repeat("d", 32)
	delivery := &usecase.ResetDeliveryUseCase{Store: f.resets, Cipher: f.cipher, ResetURL: "https://shop.example.invalid/reset-password", Log: zerolog.Nop()}
	router := transport.NewRouter("production", zerolog.Nop(), f.jwt, transport.NewAuthHandler(f.auth, zerolog.Nop(), true), transport.NewAdminHandler(usecase.NewAdminUseCase(f.users, f.refresh, f.tx), zerolog.Nop()), transport.NewInternalHandler(f.auth, zerolog.Nop()), f.accessHandler(), f.accessGuard(), transport.Security{ServiceKey: strings.Repeat("s", 32), DeliveryKey: key}, delivery)
	identity := httptest.NewServer(router)
	defer identity.Close()
	links := make(chan string, 1)
	var fail atomic.Bool
	fail.Store(true)
	notification := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reset-Delivery-Key") != key {
			t.Error("missing delivery authentication")
			w.WriteHeader(403)
			return
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["delivery_id"] == "" {
			t.Error("expected ID-only job")
			w.WriteHeader(400)
			return
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		req, _ := http.NewRequest("GET", identity.URL+"/internal/password-reset-deliveries/"+body["delivery_id"], nil)
		req.Header.Set("X-Reset-Delivery-Key", key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error("identity material unavailable")
			w.WriteHeader(503)
			return
		}
		defer res.Body.Close()
		var message usecase.ResetMessage
		if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&message) != nil {
			t.Error("invalid reset message")
			w.WriteHeader(503)
			return
		}
		links <- message.URL
		w.WriteHeader(204)
	}))
	defer notification.Close()
	delivery.Notifier = adapter.ResetNotifier{BaseURL: notification.URL, Key: key}
	if err := f.auth.RequestPasswordReset(t.Context(), "buyer@example.invalid"); err != nil {
		t.Fatal(err)
	}
	delivery.DeliverNext(t.Context())
	var status string
	var attempts int
	if err := f.pool.QueryRow(t.Context(), `SELECT status,attempts FROM password_reset_deliveries`).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 1 {
		t.Fatal("failed delivery not retained")
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE password_reset_deliveries SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	fail.Store(false)
	delivery.DeliverNext(t.Context())
	var captured string
	select {
	case captured = <-links:
	default:
		t.Fatal("delivery link missing")
	}
	if !strings.Contains(captured, "#token=") {
		t.Fatal("delivery link missing")
	}
	var remaining int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM password_reset_deliveries WHERE encrypted_token IS NOT NULL`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("successful send did not wipe material")
	}
	token := strings.Split(captured, "#token=")[1]
	if err := f.auth.ResetPassword(t.Context(), token, "synthetic-new-password"); err != nil {
		t.Fatal("delivered token cannot reset password")
	}
	if _, err := f.auth.Login(t.Context(), "buyer@example.invalid", "synthetic-new-password"); err != nil {
		t.Fatal("login after delivered reset failed")
	}
	if err := f.auth.RequestPasswordReset(t.Context(), "buyer@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE password_reset_deliveries SET expires_at=now()-interval '1 second' WHERE encrypted_token IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	delivery.DeliverNext(t.Context())
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM password_reset_deliveries WHERE encrypted_token IS NOT NULL`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("expired material not wiped")
	}
	res, err := http.Get(identity.URL + "/internal/users/" + uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("internal user lookup allowed without service credentials")
	}
}

func setup(t *testing.T) *fixture {
	t.Helper()
	raw, err := config.TestDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Skip("set IDENTITY_TEST_DATABASE_URL to the isolated identity_test database")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "identity_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal("test database unavailable")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); e != nil {
			t.Error("test schema cleanup failed")
		}
		admin.Close()
	})
	for _, name := range []string{"000002_identity_core.up.sql", "000003_identity_sessions.up.sql", "000004_audit_search.up.sql", "000005_admin_permissions.up.sql"} {
		sql, e := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(e)
		}
	}
	cipher, err := usecase.NewTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: pool, users: repository.NewUserRepository(pool), resets: repository.NewPasswordResetRepository(pool), refresh: repository.NewRefreshTokenRepository(pool), tx: repository.Transactions{Pool: pool}, cipher: cipher, jwt: authjwttest.Manager()}
	f.auth = usecase.NewAuthUseCase(f.users, f.refresh, f.resets, f.jwt, zerolog.Nop(), f.tx, cipher)
	f.jwt.SetVerifier(f.auth.ValidateSession)
	return f
}
func (f *fixture) register(t *testing.T) *usecase.AuthResult {
	t.Helper()
	r, err := f.auth.Register(t.Context(), "buyer@example.invalid", "synthetic-password-123", "Test Buyer", "buyer")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *fixture) resetToken(t *testing.T) string {
	t.Helper()
	if err := f.auth.RequestPasswordReset(t.Context(), "buyer@example.invalid"); err != nil {
		t.Fatal(err)
	}
	id, err := f.resets.ClaimDelivery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.resets.ReadDelivery(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.cipher.Decrypt(d.EncryptedToken, d.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(d.EncryptedToken, []byte(token)) {
		t.Fatal("plaintext token persisted")
	}
	return token
}
func TestIntegrationRefreshConcurrentAndLogoutFamily(t *testing.T) {
	f := setup(t)
	initial := f.register(t)
	var successes atomic.Int32
	var result *usecase.AuthResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := f.auth.RefreshToken(t.Context(), initial.RefreshToken)
			if err == nil {
				successes.Add(1)
				mu.Lock()
				result = r
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected one rotation, got %d", successes.Load())
	}
	claims, err := f.jwt.Parse(result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.auth.ValidateSession(t.Context(), claims); err != nil {
		t.Fatal("rotated session not active")
	}
	if err = f.auth.Logout(t.Context(), initial.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if err = f.auth.ValidateSession(t.Context(), claims); !errors.Is(err, authjwt.ErrInvalidToken) {
		t.Fatal("logout failed to revoke family")
	}
}
func TestIntegrationRotationRollsBackWhenReplacementInsertFails(t *testing.T) {
	f := setup(t)
	initial := f.register(t)
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$; CREATE TRIGGER reject_refresh BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION reject_refresh()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.RefreshToken(t.Context(), initial.RefreshToken); err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := f.pool.Exec(t.Context(), `DROP TRIGGER reject_refresh ON refresh_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.RefreshToken(t.Context(), initial.RefreshToken); err != nil {
		t.Fatal("failed transaction consumed old token")
	}
}
func TestIntegrationResetAtomicAndSingleUse(t *testing.T) {
	f := setup(t)
	initial := f.register(t)
	token := f.resetToken(t)
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$; CREATE TRIGGER reject_audit BEFORE INSERT ON identity_audit_logs FOR EACH ROW EXECUTE FUNCTION reject_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := f.auth.ResetPassword(t.Context(), token, "synthetic-new-password"); err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := f.auth.Login(t.Context(), "buyer@example.invalid", "synthetic-password-123"); err != nil {
		t.Fatal("password changed despite rollback")
	}
	hash := sha256.Sum256([]byte(token))
	if _, err := f.resets.FindUsableByHash(t.Context(), hex.EncodeToString(hash[:])); err != nil {
		t.Fatal("token consumed despite rollback")
	}
	if _, err := f.pool.Exec(t.Context(), `DROP TRIGGER reject_audit ON identity_audit_logs`); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.auth.ResetPassword(t.Context(), token, "synthetic-new-password") == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("expected one successful reset, got %d", wins.Load())
	}
	claims, _ := f.jwt.Parse(initial.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) == nil {
		t.Fatal("old access token still authorized")
	}
	if _, err := f.auth.RefreshToken(t.Context(), initial.RefreshToken); err == nil {
		t.Fatal("old refresh token still authorized")
	}
	if _, err := f.auth.Login(t.Context(), "buyer@example.invalid", "synthetic-new-password"); err != nil {
		t.Fatal("new password rejected")
	}
	var remaining int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM password_reset_deliveries WHERE encrypted_token IS NOT NULL`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("reset material not wiped")
	}
}
func TestIntegrationSuspendAndBootstrap(t *testing.T) {
	f := setup(t)
	buyer := f.register(t)
	if err := usecase.BootstrapAdmin(t.Context(), f.tx, f.users, "admin@example.invalid", "synthetic-admin-password", "Admin", "test-operator"); err != nil {
		t.Fatal(err)
	}
	if err := usecase.BootstrapAdmin(t.Context(), f.tx, f.users, "other@example.invalid", "synthetic-admin-password", "Admin", "test-operator"); err == nil {
		t.Fatal("second bootstrap accepted")
	}
	admin, err := f.users.FindByEmail(t.Context(), "admin@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewAdminUseCase(f.users, f.refresh, f.tx)
	if _, err = uc.SetActive(t.Context(), buyer.User.ID, buyer.User.ID, false); err == nil {
		t.Fatal("buyer changed account status")
	}
	if _, err = uc.SetActive(t.Context(), admin.ID, buyer.User.ID, false); err != nil {
		t.Fatal(err)
	}
	claims, _ := f.jwt.Parse(buyer.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) == nil {
		t.Fatal("suspended user token accepted")
	}
	if _, err = uc.SetActive(t.Context(), admin.ID, buyer.User.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.auth.ValidateSession(t.Context(), claims) == nil {
		t.Fatal("reactivation revived revoked session")
	}
}
func TestIntegrationCookieCSRFAndRateLimit(t *testing.T) {
	f := setup(t)
	initial := f.register(t)
	raw := config.TestRedisURL()
	if raw == "" {
		t.Skip("set IDENTITY_TEST_REDIS_URL")
	}
	options, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	security := transport.Security{Origins: []string{"http://localhost:3000"}, Redis: client, RateKey: uuid.NewString(), ServiceKey: strings.Repeat("s", 32), DeliveryKey: strings.Repeat("d", 32)}
	router := transport.NewRouter("production", zerolog.Nop(), f.jwt, transport.NewAuthHandler(f.auth, zerolog.Nop(), true), transport.NewAdminHandler(usecase.NewAdminUseCase(f.users, f.refresh, f.tx), zerolog.Nop()), transport.NewInternalHandler(f.auth, zerolog.Nop()), f.accessHandler(), f.accessGuard(), security, &usecase.ResetDeliveryUseCase{})
	request := func(path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.RemoteAddr = "192.0.2.5:1000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Protection", "1")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	bad := request("/api/auth/refresh", "", "https://untrusted.invalid", &http.Cookie{Name: "shopee_refresh", Value: initial.RefreshToken})
	if bad.Code != 403 {
		t.Fatal("cross-origin refresh accepted")
	}
	good := request("/api/auth/refresh", "", "http://localhost:3000", &http.Cookie{Name: "shopee_refresh", Value: initial.RefreshToken})
	if good.Code != 200 {
		t.Fatalf("refresh status %d", good.Code)
	}
	cookies := good.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/auth" {
		t.Fatal("unsafe session cookie")
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(good.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope.Data["refresh_token"]; ok {
		t.Fatal("refresh token exposed in JSON")
	}
	for range 5 {
		rec := request("/api/auth/login", `{"email":"unknown@example.invalid","password":"synthetic-password"}`, "http://localhost:3000", nil)
		if rec.Code != 401 {
			t.Fatalf("unexpected login status %d", rec.Code)
		}
	}
	if request("/api/auth/login", `{"email":"UNKNOWN@example.invalid","password":"synthetic-password"}`, "http://localhost:3000", nil).Code != 429 {
		t.Fatal("account rate limit not enforced")
	}
}
