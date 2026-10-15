package transport

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
)

// AF-16: only the CSV upload route escapes the 1 MiB JSON body cap; it
// applies its own IMPORT_MAX_BYTES limit.
func TestOnlyImportUploadSkipsDefaultBodyCap(t *testing.T) {
	r := NewRouter("test", zerolog.Nop(), authjwttest.Manager(), &ItemHandler{}, &InternalHandler{}, &AdminHandler{}, adminaccesstest.Guard(AdminRoutes), serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret"))
	read := func(c *gin.Context) {
		n, err := io.Copy(io.Discard, c.Request.Body)
		if err != nil {
			c.String(413, "cut")
			return
		}
		c.String(200, strconv.FormatInt(n, 10))
	}
	r.POST(ImportUploadPath, read)
	r.POST("/api/inventory/vendor/other", read)
	body := bytes.Repeat([]byte("a"), 2<<20)
	for path, want := range map[string]int{ImportUploadPath: 200, "/api/inventory/vendor/other": 413} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestBulkRoutesFitRouterAndAdminBundles(t *testing.T) {
	jwt := authjwttest.Manager()
	guard := adminaccesstest.Guard(AdminRoutes)
	r := NewRouter("test", zerolog.Nop(), jwt, &ItemHandler{}, &InternalHandler{}, &AdminHandler{}, guard, serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret"))
	RegisterStockSelfService(r, jwt, guard, StockSelfServiceHandler{})
	bulkimport.Register(r.Group("/api/inventory/vendor", middleware.RequireAuth(jwt), middleware.SellerConsole()),
		r.Group("/api/inventory/admin", middleware.RequireAuth(jwt), middleware.RequireRole("admin"), guard), bulkimport.Handler{S: &bulkimport.Service{}})
	for _, route := range r.Routes() {
		if strings.HasPrefix(route.Path, "/api/inventory/admin/import-jobs") {
			if _, ok := AdminRoutes[route.Method+" "+route.Path]; !ok {
				t.Errorf("admin route without bundle: %s %s", route.Method, route.Path)
			}
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", ImportUploadPath, nil))
	if w.Code != 401 {
		t.Fatalf("anonymous upload: %d", w.Code)
	}
}
