package transport

import (
	"net/http/httptest"
	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/middleware"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func TestBusinessReportAdminPermission(t *testing.T) {
	for _, suffix := range []string{""} {
		path := "/api/admin/business-reports" + suffix
		if AdminRoutes["GET "+path] != adminaccess.AnalyticsRead {
			t.Fatalf("wrong permission for %s", path)
		}
		for _, allowed := range []bool{false, true} {
			checker := &adminaccesstest.Checker{Grants: []string{adminaccess.AuditRead}}
			if allowed {
				checker.Grants = []string{adminaccess.AnalyticsRead}
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(middleware.ContextKeyUserID, "fake-admin")
				c.Set(middleware.ContextKeyRole, "admin")
			})
			r.Use(adminaccess.Guard(checker, AdminRoutes, zerolog.Nop()))
			r.GET(path, func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			want := 403
			if allowed {
				want = 204
			}
			if w.Code != want || len(checker.Asked) != 1 || checker.Asked[0] != adminaccess.AnalyticsRead {
				t.Fatalf("allowed=%v status=%d checked=%v", allowed, w.Code, checker.Asked)
			}
		}
	}
}
