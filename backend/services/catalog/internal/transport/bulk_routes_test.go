package transport

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
)

// AF-16 routes are mounted in main next to the router's own routes; they
// must fit the route tree, pass the identifier validation and every admin
// route needs a permission bundle.
func TestBulkRoutesFitRouterAndAdminBundles(t *testing.T) {
	jwt := authjwttest.Manager()
	key := serviceauth.SharedKey("fake-test-service-key-not-a-real-secret")
	r := NewRouter("test", zerolog.Nop(), jwt, &CategoryHandler{}, &ProductHandler{}, &StorefrontHandler{}, &AdminHandler{}, &InternalHandler{}, &AttributeHandler{}, adminaccesstest.Guard(AdminRoutes), key)
	bulkimport.Register(r.Group("/api/catalog/vendor", middleware.RequireAuth(jwt), middleware.SellerConsole()),
		r.Group("/api/catalog/admin", middleware.RequireAuth(jwt), middleware.RequireRole("admin"), adminaccesstest.Guard(AdminRoutes)), bulkimport.Handler{S: &bulkimport.Service{}})
	r.POST("/internal/catalog/vendor-targets", key.Allow("inventory"), BulkTargetsHandler{}.Lookup)
	for _, route := range r.Routes() {
		if strings.HasPrefix(route.Path, "/api/catalog/admin/import-jobs") {
			if _, ok := AdminRoutes[route.Method+" "+route.Path]; !ok {
				t.Errorf("admin route without bundle: %s %s", route.Method, route.Path)
			}
		}
	}
	serve := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		token, _, err := jwt.IssueAccessToken("00000000-0000-0000-0000-000000000009", "vendor", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	// A template name passes the UUID check; a bad job id does not.
	if code := serve("GET", "/api/catalog/vendor/import-jobs/not-a-uuid"); code != 400 {
		t.Fatalf("invalid job id: %d", code)
	}
	if code := serve("GET", "/api/catalog/vendor/import-templates/prices?vendor_id=bad"); code != 400 {
		t.Fatalf("invalid vendor id: %d", code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/internal/catalog/vendor-targets", strings.NewReader(`{}`)))
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("unprotected target lookup: %d", w.Code)
	}
}
