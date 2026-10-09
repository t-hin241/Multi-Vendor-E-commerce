package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/notification/internal/transport"
	"shopee/backend/services/notification/internal/usecase"
)

// The notify contract needs the internal key (it used to be open), and
// every admin route needs an admin token.
func TestRoutesNeedServiceKeyOrAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwttest.Manager()
	key := "test-internal-key-not-a-real-secret-0000"
	router := transport.NewRouter("test", zerolog.Nop(), jwt, serviceauth.SharedKey(key), transport.NewInternalHandler(nil, zerolog.Nop()), transport.NewAdminHandler(nil, zerolog.Nop()), adminaccesstest.Guard(transport.AdminRoutes))
	send := func(method, path string, headers map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	for _, h := range []map[string]string{nil, {serviceauth.Header: "wrong-key"}} {
		if code := send("POST", "/internal/notifications", h); code != http.StatusForbidden {
			t.Fatalf("notify without the key: %d", code)
		}
	}
	if code := send("POST", "/internal/notifications", map[string]string{serviceauth.Header: key}); code != http.StatusBadRequest {
		t.Fatalf("with the key an empty body is a validation error, got %d", code)
	}
	buyer, _, _ := jwt.IssueAccessToken("11111111-1111-1111-1111-111111111111", "buyer", time.Minute)
	for _, route := range [][2]string{{"GET", "/api/notifications/admin"}, {"GET", "/api/notifications/admin/operations"},
		{"POST", "/api/notifications/admin/11111111-1111-1111-1111-111111111111/retry"}} {
		if code := send(route[0], route[1], nil); code != http.StatusUnauthorized {
			t.Fatalf("%v without token: %d", route, code)
		}
		if code := send(route[0], route[1], map[string]string{"Authorization": "Bearer " + buyer}); code != http.StatusForbidden {
			t.Fatalf("%v as buyer: %d", route, code)
		}
	}
}

// AF-19: every admin route names the permission bundle it needs.
func TestEveryAdminRouteNamesAPermission(t *testing.T) {
	router := transport.NewRouter("test", zerolog.Nop(), authjwttest.Manager(), serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret"),
		transport.NewInternalHandler(nil, zerolog.Nop()), transport.NewAdminHandler(nil, zerolog.Nop()), adminaccesstest.Guard(transport.AdminRoutes))
	adminaccesstest.AssertCovered(t, router, transport.AdminRoutes)
}

// AF-08: the report route needs a service key, preferences a session (any
// role), the review routes an admin with a permission bundle.
func TestVendorActionRoutesAreProtected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwttest.Manager()
	key := "test-internal-key-not-a-real-secret-0000"
	router := gin.New()
	transport.RegisterVendorActions(router, jwt, serviceauth.SharedKey(key), adminaccesstest.Guard(transport.AdminRoutes),
		transport.VendorActionRoutes{UseCase: &usecase.VendorActionUseCase{}, Log: zerolog.Nop()})
	adminaccesstest.AssertCovered(t, router, transport.AdminRoutes)
	send := func(method, path string, headers map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("POST", "/internal/vendor-action-notices", nil); code != http.StatusForbidden {
		t.Fatalf("report without the key: %d", code)
	}
	if code := send("POST", "/internal/vendor-action-notices", map[string]string{serviceauth.Header: key}); code != http.StatusBadRequest {
		t.Fatalf("report with the key and an empty body: %d", code)
	}
	for _, route := range [][2]string{{"GET", "/api/notifications/preferences"}, {"PATCH", "/api/notifications/preferences"}} {
		if code := send(route[0], route[1], nil); code != http.StatusUnauthorized {
			t.Fatalf("%v without a session: %d", route, code)
		}
	}
	vendor, _, _ := jwt.IssueAccessToken("11111111-1111-1111-1111-111111111111", "vendor", time.Minute)
	if code := send("GET", "/api/notifications/preferences", map[string]string{"Authorization": "Bearer " + vendor}); code != http.StatusNotFound {
		t.Fatalf("preferences while the feature is off: %d", code)
	}
	for _, route := range [][2]string{{"GET", "/api/notifications/admin/vendor-actions"}, {"GET", "/api/notifications/admin/vendor-actions/summary"},
		{"POST", "/api/notifications/admin/vendor-actions/11111111-1111-1111-1111-111111111111/retry"}} {
		if code := send(route[0], route[1], nil); code != http.StatusUnauthorized {
			t.Fatalf("%v without token: %d", route, code)
		}
		if code := send(route[0], route[1], map[string]string{"Authorization": "Bearer " + vendor}); code != http.StatusForbidden {
			t.Fatalf("%v as vendor: %d", route, code)
		}
	}
}

// AF-09: the inbox needs a session (any role), answers feature_disabled
// while off, and is never cached.
func TestInboxRoutesNeedASession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwttest.Manager()
	router := gin.New()
	transport.RegisterInbox(router, jwt, transport.InboxRoutes{UseCase: &usecase.InboxUseCase{}, Log: zerolog.Nop()})
	routes := [][2]string{{"GET", "/api/notifications/inbox"}, {"GET", "/api/notifications/inbox/unread-count"},
		{"POST", "/api/notifications/inbox/read-markers"}, {"PUT", "/api/notifications/inbox/11111111-1111-1111-1111-111111111111/read"},
		{"DELETE", "/api/notifications/inbox/11111111-1111-1111-1111-111111111111"}}
	admin, _, _ := jwt.IssueAccessToken("11111111-1111-1111-1111-111111111111", "admin", time.Minute)
	for _, route := range routes {
		for _, auth := range []string{"", "Bearer " + admin} {
			req := httptest.NewRequest(route[0], route[1], strings.NewReader(`{"through_id":"11111111-1111-1111-1111-111111111111"}`))
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			want := http.StatusUnauthorized
			if auth != "" {
				want = http.StatusNotFound
				if w.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("%v is cacheable", route)
				}
			}
			if w.Code != want {
				t.Fatalf("%v signed in=%v: %d", route, auth != "", w.Code)
			}
		}
	}
}
