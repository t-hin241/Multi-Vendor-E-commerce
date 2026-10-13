package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/config"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
	"shopee/backend/services/identity/internal/transport"
	"shopee/backend/services/identity/internal/usecase"
)

func (f *fixture) account() *usecase.AccountUseCase {
	return &usecase.AccountUseCase{Store: repository.AccountRepository{Pool: f.pool}, Users: f.users, Refresh: f.refresh, Resets: f.resets, Tx: f.tx, Cipher: f.cipher, Enabled: true, VerifyTTL: 30 * time.Minute}
}
func (f *fixture) actor(t *testing.T, result *usecase.AuthResult) usecase.AccountActor {
	t.Helper()
	claims, err := f.jwt.Parse(result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return usecase.AccountActor{UserID: claims.UserID, Role: claims.Role, SessionID: claims.SessionID}
}
func (f *fixture) changeToken(t *testing.T, userID string) string {
	t.Helper()
	var encrypted []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT encrypted_token FROM password_reset_deliveries WHERE user_id=$1 AND kind='email_change' AND status='pending'`, userID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	raw, err := f.cipher.Decrypt(encrypted, userID)
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Token string }
	if err = json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	return body.Token
}
func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var app *apperror.Error
	if !errors.As(err, &app) || app.Status != status {
		t.Fatalf("want status %d, got %v", status, err)
	}
}

func TestAccountProfileCASAndRolePreservation(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := uc.Profile(t.Context(), a, domain.ProfileUpdate{FullName: "Updated Buyer", Phone: "+84912345678", ExpectedVersion: 1})
			if err == nil {
				wins.Add(1)
			} else {
				assertStatus(t, err, 409)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("CAS did not choose exactly one writer")
	}
	saved, err := f.users.FindByID(t.Context(), a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProfileVersion != 2 || saved.Role != domain.RoleBuyer || saved.Phone != "+84912345678" {
		t.Fatal("profile or role incorrect")
	}
	uc.Enabled = false
	if _, err = uc.Profile(t.Context(), a, domain.ProfileUpdate{FullName: "Off", ExpectedVersion: 2}); err == nil {
		t.Fatal("disabled update accepted")
	}
}

func TestAccountPasswordRevokesAndReauth(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	reset := f.resetToken(t)
	if err := uc.RequestEmailChange(t.Context(), a, "new@example.invalid", "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	change := f.changeToken(t, a.UserID)
	assertStatus(t, uc.ChangePassword(t.Context(), a, "synthetic-wrong-password", "synthetic-new-password"), 422)
	if _, err := f.auth.Login(t.Context(), user.User.Email, "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	if err := uc.ChangePassword(t.Context(), a, "synthetic-password-123", "synthetic-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.RefreshToken(t.Context(), user.RefreshToken); err == nil {
		t.Fatal("old refresh accepted")
	}
	claims, _ := f.jwt.Parse(user.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) == nil {
		t.Fatal("old access accepted")
	}
	if err := f.auth.ResetPassword(t.Context(), reset, "synthetic-another-password"); err == nil {
		t.Fatal("old reset accepted")
	}
	if err := uc.ConfirmEmailChange(t.Context(), change); err == nil {
		t.Fatal("old email change accepted")
	}
	if _, err := f.auth.Login(t.Context(), user.User.Email, "synthetic-new-password"); err != nil {
		t.Fatal(err)
	}
	saved, _ := f.users.FindByID(t.Context(), a.UserID)
	if saved.AuthVersion != 2 {
		t.Fatal("auth version not bumped")
	}
}

func TestAccountSessionsOwnershipPaginationAndRevocation(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	other, err := f.auth.Register(t.Context(), "other@example.invalid", "synthetic-password-123", "Other", "vendor")
	if err != nil {
		t.Fatal(err)
	}
	b := f.actor(t, other)
	if err = uc.Revoke(t.Context(), a, b.SessionID, false); err != nil {
		t.Fatal(err)
	}
	claims, _ := f.jwt.Parse(other.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) != nil {
		t.Fatal("cross-user revoke")
	}
	second, err := f.auth.Login(t.Context(), user.User.Email, "synthetic-password-123")
	if err != nil {
		t.Fatal(err)
	}
	page, err := uc.Sessions(t.Context(), a, "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("missing first page", err)
	}
	next, err := uc.Sessions(t.Context(), a, page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || !next.Items[0].Current {
		t.Fatal("invalid second page", err)
	}
	foreign, err := uc.Sessions(t.Context(), b, page.NextCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range foreign.Items {
		if s.ID == a.SessionID {
			t.Fatal("cursor bypassed ownership")
		}
	}
	if err = uc.Revoke(t.Context(), a, f.actor(t, second).SessionID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = f.auth.RefreshToken(t.Context(), second.RefreshToken); err == nil {
		t.Fatal("revoked session refreshed")
	}
	if err = uc.Revoke(t.Context(), a, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err = uc.Sessions(t.Context(), a, "", 20); err == nil {
		t.Fatal("revoked actor could list")
	}
}

func TestAccountEmailChangeConcurrentConsumeDeliveryAndPurpose(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	reset := f.resetToken(t)
	if err := uc.ConfirmEmailChange(t.Context(), reset); err == nil {
		t.Fatal("reset used as email change")
	}
	if err := uc.RequestEmailChange(t.Context(), a, " New@Example.invalid ", "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	token := f.changeToken(t, a.UserID)
	if err := f.auth.ResetPassword(t.Context(), token, "synthetic-other-password"); err == nil {
		t.Fatal("change used as reset")
	}
	verify := &usecase.EmailVerificationUseCase{Store: repository.EmailVerificationRepository{Pool: f.pool}, Users: f.users, Tx: f.tx, Cipher: f.cipher, Enabled: true, Log: zerolog.Nop()}
	if err := verify.Confirm(t.Context(), token); err == nil {
		t.Fatal("change used as verification")
	}
	before, _ := f.users.FindByID(t.Context(), a.UserID)
	if before.Email != "buyer@example.invalid" {
		t.Fatal("unconfirmed address changed")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := uc.ConfirmEmailChange(t.Context(), token)
			if err == nil {
				wins.Add(1)
			} else {
				assertStatus(t, err, 400)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("email change consumed more than once")
	}
	saved, _ := f.users.FindByID(t.Context(), a.UserID)
	if saved.Email != "new@example.invalid" || saved.AuthVersion != 2 || saved.EmailVerifiedAt == nil {
		t.Fatal("new email not committed")
	}
	if _, err := f.auth.Login(t.Context(), user.User.Email, "synthetic-password-123"); err == nil {
		t.Fatal("old email login accepted")
	}
	if _, err := f.auth.Login(t.Context(), saved.Email, "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	if err := f.auth.ResetPassword(t.Context(), reset, "synthetic-other-password"); err == nil {
		t.Fatal("reset survived change")
	}
	claims, _ := f.jwt.Parse(user.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) == nil {
		t.Fatal("access survived email change")
	}
	delivery := &usecase.ResetDeliveryUseCase{Store: f.resets, Cipher: f.cipher, ChangeURL: "https://shop.example.invalid/change-email"}
	id, err := f.resets.ClaimDelivery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	message, err := delivery.Message(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if message.Kind != "email_changed" || message.Email != "buyer@example.invalid" || message.URL != "" {
		t.Fatal("previous-address notice incorrect")
	}
	if err = f.resets.FinishDelivery(t.Context(), id, false); err != nil {
		t.Fatal(err)
	}
	var status string
	var n int
	if err = f.pool.QueryRow(t.Context(), `SELECT status,octet_length(encrypted_token) FROM password_reset_deliveries WHERE id=$1`, id).Scan(&status, &n); err != nil || status != "pending" || n == 0 {
		t.Fatal("notice not retryable", err)
	}
}

func TestAccountEmailChangeUniquenessReplacementExpiryAndReset(t *testing.T) {
	f := setup(t)
	first := f.register(t)
	second, err := f.auth.Register(t.Context(), "second@example.invalid", "synthetic-password-123", "Second", "vendor")
	if err != nil {
		t.Fatal(err)
	}
	uc := f.account()
	a, b := f.actor(t, first), f.actor(t, second)
	for _, actor := range []usecase.AccountActor{a, b} {
		if err = uc.RequestEmailChange(t.Context(), actor, "same@example.invalid", "synthetic-password-123"); err != nil {
			t.Fatal(err)
		}
	}
	tokens := []string{f.changeToken(t, a.UserID), f.changeToken(t, b.UserID)}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := uc.ConfirmEmailChange(t.Context(), token)
			if err == nil {
				wins.Add(1)
			} else {
				assertStatus(t, err, 400)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("unique email race failed")
	}
	// A fresh fixture isolates replacement/expiry from the winning account.
	g := setup(t)
	user := g.register(t)
	a = g.actor(t, user)
	uc = g.account()
	if err = uc.RequestEmailChange(t.Context(), a, "first@example.invalid", "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	old := g.changeToken(t, a.UserID)
	assertStatus(t, uc.RequestEmailChange(t.Context(), a, "second@example.invalid", "synthetic-password-123"), 429)
	if _, err = g.pool.Exec(t.Context(), `UPDATE email_change_operations SET created_at=now()-interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	if err = uc.RequestEmailChange(t.Context(), a, "second@example.invalid", "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, uc.ConfirmEmailChange(t.Context(), old), 400)
	fresh := g.changeToken(t, a.UserID)
	if _, err = g.pool.Exec(t.Context(), `UPDATE email_change_operations SET expires_at=now()-interval '1 second' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, uc.ConfirmEmailChange(t.Context(), fresh), 400)
	if _, err = g.pool.Exec(t.Context(), `UPDATE email_change_operations SET expires_at=now()+interval '30 minutes' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	// Queue may contain another kind; obtain only the reset material here.
	if err = g.auth.RequestPasswordReset(t.Context(), "buyer@example.invalid"); err != nil {
		t.Fatal(err)
	}
	var encryptedReset []byte
	if err = g.pool.QueryRow(t.Context(), `SELECT encrypted_token FROM password_reset_deliveries WHERE kind='password_reset' AND status='pending'`).Scan(&encryptedReset); err != nil {
		t.Fatal(err)
	}
	reset, err := g.cipher.Decrypt(encryptedReset, a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.auth.ResetPassword(t.Context(), reset, "synthetic-next-password"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, uc.ConfirmEmailChange(t.Context(), fresh), 400)
}

func TestAccountVerificationRateTTLAndConcurrentConsume(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	v := &usecase.EmailVerificationUseCase{Store: repository.EmailVerificationRepository{Pool: f.pool}, Users: f.users, Tx: f.tx, Cipher: f.cipher, Enabled: true, TTL: 30 * time.Minute, Log: zerolog.Nop()}
	for i := 0; i < 5; i++ {
		if i > 0 {
			if _, err := f.pool.Exec(t.Context(), `UPDATE email_verification_tokens SET created_at=created_at-interval '61 seconds'`); err != nil {
				t.Fatal(err)
			}
		}
		if err := v.Resend(t.Context(), user.User.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_verification_tokens SET created_at=created_at-interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, v.Resend(t.Context(), user.User.ID), 429)
	var ttl time.Time
	var encrypted []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT expires_at,encrypted_token FROM password_reset_deliveries WHERE kind='email_verification' AND status='pending'`).Scan(&ttl, &encrypted); err != nil {
		t.Fatal(err)
	}
	if time.Until(ttl) > 30*time.Minute || time.Until(ttl) < 29*time.Minute {
		t.Fatal("incorrect verification TTL")
	}
	token, err := f.cipher.Decrypt(encrypted, user.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.auth.ResetPassword(t.Context(), token, "synthetic-new-password"); err == nil {
		t.Fatal("verification token used as reset")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := v.Confirm(t.Context(), token)
			if err == nil {
				wins.Add(1)
			} else {
				assertStatus(t, err, 400)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("verification race failed")
	}
}

func TestAccountHTTPGuardsAndSafeResponses(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	raw := config.TestRedisURL()
	if raw == "" {
		t.Skip("set IDENTITY_TEST_REDIS_URL")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	security := transport.Security{Origins: []string{"http://localhost:3000"}, Redis: client, RateKey: uuid.NewString()}
	r := gin.New()
	transport.RegisterAccount(r, f.jwt, security, transport.AccountHandler{UseCase: f.account(), Log: zerolog.Nop(), Secure: true})
	request := func(method, path, body, token string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if csrf {
			req.Header.Set("Origin", "http://localhost:3000")
			req.Header.Set("X-CSRF-Protection", "1")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if res := request("PATCH", "/api/auth/me", `{"full_name":"Hijack","phone":"","expected_version":1}`, user.AccessToken, false); res.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if res := request("PATCH", "/api/auth/me", `{"full_name":"Hijack","role":"admin","expected_version":1}`, user.AccessToken, true); res.Code != 400 {
		t.Fatal("role assignment accepted")
	}
	if res := request("GET", "/api/auth/sessions", "", "", false); res.Code != 401 {
		t.Fatal("anonymous sessions accepted")
	}
	for _, extra := range []string{`"role":"admin"`, `"user_id":"someone-else"`, `"email":"other@example.invalid"`} {
		if res := request("PATCH", "/api/auth/me", `{"full_name":"Safe","phone":"","expected_version":1,`+extra+`}`, user.AccessToken, true); res.Code != 400 {
			t.Fatal("extra field accepted")
		}
	}
	res := request("PATCH", "/api/auth/me", `{"full_name":"Safe","phone":"","expected_version":1}`, user.AccessToken, true)
	if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" || strings.Contains(res.Body.String(), "password") || strings.Contains(res.Body.String(), "auth_version") {
		t.Fatal("unsafe response", res.Code)
	}
	res = request("POST", "/api/auth/password-changes", `{"current_password":"synthetic-password-123","new_password":"synthetic-new-password"}`, user.AccessToken, true)
	if res.Code != 200 || !strings.Contains(res.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("cookie not cleared", res.Code)
	}
	if res = request("GET", "/api/auth/sessions", "", user.AccessToken, false); res.Code != 401 {
		t.Fatal("revoked access accepted")
	}
}

func TestAccountEmailDeliveryAndPurge(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	if err := uc.RequestEmailChange(t.Context(), a, "new@example.invalid", "synthetic-password-123"); err != nil {
		t.Fatal(err)
	}
	id, err := f.resets.ClaimDelivery(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	delivery := &usecase.ResetDeliveryUseCase{Store: f.resets, Cipher: f.cipher, ChangeURL: "https://shop.example.invalid/change-email"}
	message, err := delivery.Message(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(message.URL)
	if err != nil {
		t.Fatal(err)
	}
	if message.Email != "new@example.invalid" || message.Kind != "email_change" || link.RawQuery != "" || !strings.HasPrefix(link.Fragment, "token=") {
		t.Fatal("unsafe delivery")
	}
	if err = f.resets.FinishDelivery(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = delivery.Message(t.Context(), id); err == nil {
		t.Fatal("material accessible outside lease")
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE email_change_operations SET expires_at=now()-interval '2 days'; UPDATE password_reset_deliveries SET expires_at=now()-interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if err = (repository.AccountRepository{Pool: f.pool}).Purge(t.Context(), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM email_change_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired material not purged", err)
	}
}

// Inject an audit failure after the writes to prove transaction rollback.
type failingAccountAudit struct{ usecase.UserRepository }

func (f failingAccountAudit) Audit(context.Context, string, string, string, string) error {
	return errors.New("synthetic audit failure")
}
func TestAccountPasswordTransactionRollback(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	uc := f.account()
	uc.Users = failingAccountAudit{f.users}
	if err := uc.ChangePassword(t.Context(), f.actor(t, user), "synthetic-password-123", "synthetic-new-password"); err == nil {
		t.Fatal("expected rollback")
	}
	if _, err := f.auth.Login(t.Context(), user.User.Email, "synthetic-password-123"); err != nil {
		t.Fatal("password not rolled back")
	}
	if _, err := f.auth.RefreshToken(t.Context(), user.RefreshToken); err != nil {
		t.Fatal("revocation not rolled back")
	}
}

func TestAccountEmailChangeRollbackAndHourlyLimit(t *testing.T) {
	f := setup(t)
	user := f.register(t)
	a := f.actor(t, user)
	uc := f.account()
	for i := 0; i < 5; i++ {
		if i > 0 {
			if _, err := f.pool.Exec(t.Context(), `UPDATE email_change_operations SET created_at=created_at-interval '61 seconds'`); err != nil {
				t.Fatal(err)
			}
		}
		if err := uc.RequestEmailChange(t.Context(), a, "new@example.invalid", "synthetic-password-123"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE email_change_operations SET created_at=created_at-interval '61 seconds'`); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, uc.RequestEmailChange(t.Context(), a, "new@example.invalid", "synthetic-password-123"), 429)
	token := f.changeToken(t, a.UserID)
	uc.Users = failingAccountAudit{f.users}
	if err := uc.ConfirmEmailChange(t.Context(), token); err == nil {
		t.Fatal("audit failure not propagated")
	}
	saved, err := f.users.FindByID(t.Context(), a.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Email != user.User.Email || saved.AuthVersion != 1 {
		t.Fatal("email not rolled back")
	}
	claims, _ := f.jwt.Parse(user.AccessToken)
	if f.auth.ValidateSession(t.Context(), claims) != nil {
		t.Fatal("revocation not rolled back")
	}
	var notices int
	if err = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM password_reset_deliveries WHERE kind='email_changed'`).Scan(&notices); err != nil || notices != 0 {
		t.Fatal("notice not rolled back", err)
	}
	uc.Users = f.users
	if err = uc.ConfirmEmailChange(t.Context(), token); err != nil {
		t.Fatal("token not rolled back", err)
	}
}
