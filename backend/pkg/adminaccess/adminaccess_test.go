package adminaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
)

const admin = "11111111-1111-4111-8111-111111111111"

func code(err error) apperror.Code {
	if app, ok := err.(*apperror.Error); ok {
		return app.Code
	}
	return ""
}

func identity(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func TestRequireFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       apperror.Code
	}{
		{"allowed", `{"data":{"allowed":true,"user_id":"` + admin + `","permission_version":4}}`, 200, ""},
		{"refused", `{"data":{"allowed":false,"user_id":"` + admin + `"}}`, 200, CodeMissingPermission},
		{"another user", `{"data":{"allowed":true,"user_id":"someone"}}`, 200, CodeAuthUnavailable},
		{"unreadable", `nope`, 200, CodeAuthUnavailable},
		{"outage", `{}`, 503, CodeAuthUnavailable},
		{"bad request", `{}`, 400, CodeAuthUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := identity(t, tc.status, tc.body)
			defer s.Close()
			v, err := (Client{URL: s.URL, Key: "test-key"}).Require(t.Context(), admin, FinanceApprove)
			if code(err) != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if tc.want == "" && v != 4 {
				t.Fatal("permission version not carried")
			}
		})
	}
	if _, err := (Client{URL: "http://127.0.0.1:1"}).Require(t.Context(), admin, "finance.everything"); code(err) != CodeMissingPermission {
		t.Fatal("unknown bundle must be refused without asking")
	}
}

func TestConsumeProofMapsRefusals(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   apperror.Code
	}{{200, ""}, {403, CodeReauthRequired}, {409, CodeReauthRequired}, {500, CodeAuthUnavailable}} {
		s := identity(t, tc.status, `{}`)
		err := (Client{URL: s.URL}).ConsumeProof(t.Context(), "proof", admin, "payment.approval.decide", "abc")
		s.Close()
		if code(err) != tc.want {
			t.Fatalf("status %d: got %v want %s", tc.status, err, tc.want)
		}
	}
	if err := (Client{URL: "http://127.0.0.1:1"}).ConsumeProof(t.Context(), " ", admin, "p.x", "abc"); code(err) != CodeReauthRequired {
		t.Fatal("empty proof must be refused without asking")
	}
}

type fixed struct {
	grants []string
	err    error
}

func (f fixed) Require(_ context.Context, _ string, permission string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	for _, g := range f.grants {
		if g == permission {
			return 7, nil
		}
	}
	return 0, Missing(permission)
}

func TestGuardDeniesByDefault(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	routes := Routes{"POST /api/x/admin/refunds/:id/resolve": FinancePrepare}
	serve := func(checker Checker, method, path string) (int, string) {
		r := gin.New()
		g := r.Group("/api/x/admin", func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, admin); c.Next() }, Guard(checker, routes, zerolog.Nop()))
		g.POST("/refunds/:id/resolve", func(c *gin.Context) { c.String(200, fmt.Sprint(PermissionVersion(c))) })
		g.POST("/unmapped", func(c *gin.Context) { c.String(200, "ran") })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code, w.Body.String()
	}
	if got, body := serve(fixed{grants: []string{FinancePrepare}}, "POST", "/api/x/admin/refunds/1/resolve"); got != 200 || body != "7" {
		t.Fatalf("granted route refused: %d %s", got, body)
	}
	if got, body := serve(fixed{grants: []string{FinanceRead}}, "POST", "/api/x/admin/refunds/1/resolve"); got != 403 || !strings.Contains(body, string(CodeMissingPermission)) {
		t.Fatalf("missing bundle allowed: %d %s", got, body)
	}
	if got, _ := serve(fixed{grants: Bundles()}, "POST", "/api/x/admin/unmapped"); got != 403 {
		t.Fatalf("unmapped route allowed: %d", got)
	}
	if got, _ := serve(fixed{err: Unavailable(fmt.Errorf("down"))}, "POST", "/api/x/admin/refunds/1/resolve"); got != 503 {
		t.Fatalf("identity outage must fail closed with 503: %d", got)
	}
	if got, _ := serve(nil, "POST", "/api/x/admin/refunds/1/resolve"); got != 403 {
		t.Fatalf("missing checker must refuse: %d", got)
	}
}

func TestSharedRoutesAndRegistry(t *testing.T) {
	r := Shared("/api/p/admin", true, true, true)
	if r["GET /api/p/admin/audit-events"] != AuditRead || r["POST /api/p/admin/work-items/:workItemID/extensions"] != SupportManage ||
		r["POST /api/p/admin/events/:consumer/:eventId/replay"] != PlatformConfigure {
		t.Fatal("shared routes mapped wrongly")
	}
	if len(Bundles()) != 9 || Known("admin") || !Known(AccessManage) {
		t.Fatal("bundle registry wrong")
	}
	b, _ := json.Marshal(Missing(FinanceApprove).Message)
	if !strings.Contains(string(b), FinanceApprove) {
		t.Fatal("refusal must name the bundle")
	}
}
