package transport

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"

	"github.com/rs/zerolog"
)

func TestSelfServiceRoutesProtectedAndStrictInput(t *testing.T) {
	jwt := authjwttest.Manager()
	guard := adminaccesstest.Guard(AdminRoutes)
	r := NewRouter("test", zerolog.Nop(), jwt, &ItemHandler{}, &InternalHandler{}, &AdminHandler{}, guard, serviceauth.SharedKey("fake-test-key-not-a-secret"))
	RegisterStockSelfService(r, jwt, guard, StockSelfServiceHandler{Log: zerolog.Nop()})
	adminaccesstest.AssertCovered(t, r, AdminRoutes)
	for _, tc := range []struct {
		method, path, role, body string
		want                     int
	}{
		{"POST", "/api/inventory/vendor/stock-adjustments", "", "{}", 401},
		{"POST", "/api/inventory/vendor/stock-adjustments", "admin", "{}", 403},
		{"GET", "/api/inventory/admin/stock-adjustments", "buyer", "", 403},
		{"POST", "/api/inventory/admin/vendors/11111111-1111-4111-8111-111111111111/self-service-policy", "vendor", "{}", 403},
		{"POST", "/api/inventory/vendor/stock-adjustments", "buyer", `{"vendor_id":"spoof"}`, 422},
		{"POST", "/api/inventory/vendor/stock-adjustments", "vendor", `{"kind":"return"}`, 422},
		{"POST", "/api/inventory/vendor/stock-adjustments", "vendor", `{"kind":"count"}`, 422},
		{"POST", "/api/inventory/vendor/stock-counts", "vendor", `{"physical_count":1.5}`, 422},
		{"POST", "/api/inventory/vendor/stock-counts", "vendor", `{} {}`, 422},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.role != "" {
			token, _, err := jwt.IssueAccessToken("11111111-1111-4111-8111-111111111111", tc.role, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s %s %s: %d %s", tc.method, tc.path, tc.role, w.Code, w.Body.String())
		}
	}
}
