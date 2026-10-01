package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/notification/internal/transport"
)

// The notify contract needs the internal key (it used to be open), and
// every admin route needs an admin token.
func TestRoutesNeedServiceKeyOrAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwt.NewManager("test-signing-secret-not-a-real-secret-000")
	key := "test-internal-key-not-a-real-secret-0000"
	router := transport.NewRouter("test", zerolog.Nop(), jwt, key, transport.NewInternalHandler(nil, zerolog.Nop()), transport.NewAdminHandler(nil, zerolog.Nop()))
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
