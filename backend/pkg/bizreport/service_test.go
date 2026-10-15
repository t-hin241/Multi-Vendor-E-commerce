package bizreport

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
)

type reportSource struct {
	calls   int
	vendor  string
	timeout bool
}

func (s *reportSource) Report(ctx context.Context, r Range) (any, error) {
	s.calls++
	s.vendor = r.VendorID
	if s.timeout {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return map[string]string{"vendor_id": r.VendorID}, nil
}
func (s *reportSource) Drill(ctx context.Context, d Drilldown) (any, error) {
	return s.Report(ctx, d.Range)
}
func (*reportSource) DrillMetrics() []string { return []string{"vendor_orders"} }

type reportRoles struct{ allowed bool }

func (r reportRoles) RequireRole(_ context.Context, actor, role string) error {
	if !r.allowed || actor != "admin" || role != "admin" {
		return apperror.Forbidden("not admin")
	}
	return nil
}

func TestReportServiceRejectsCrossShopAndUnverifiedAdmin(t *testing.T) {
	for _, drill := range []bool{false, true} {
		for _, vendor := range []string{"", "other-shop", "own-shop"} {
			source := &reportSource{}
			s := Service{Source: source, Enabled: true, Shops: func(_ context.Context, actor, shop string) error {
				if actor != "owner" || shop != "own-shop" {
					return apperror.Forbidden("not member")
				}
				return nil
			}}
			r := Range{VendorID: vendor}
			var err error
			if drill {
				_, err = s.ShopDrill(t.Context(), "owner", Drilldown{Range: r})
			} else {
				_, err = s.ShopReport(t.Context(), "owner", r)
			}
			if vendor == "own-shop" {
				if err != nil || source.calls != 1 || source.vendor != vendor {
					t.Fatalf("own scope: %v %+v", err, source)
				}
			} else if err == nil || source.calls != 0 {
				t.Fatalf("cross scope: %v %+v", err, source)
			}
		}
	}
	for _, roles := range []RoleChecker{nil, reportRoles{}} {
		source := &reportSource{}
		s := Service{Source: source, Enabled: true, Admins: roles}
		if _, err := s.AdminReport(t.Context(), "admin", Range{}); err == nil {
			t.Fatal("unverified admin report")
		}
		if _, err := s.AdminDrill(t.Context(), "admin", Drilldown{}); err == nil {
			t.Fatal("unverified admin drill")
		}
		if source.calls != 0 {
			t.Fatal("queried source before authorization")
		}
	}
}

func TestReportServiceTimeoutAndFeatureFlag(t *testing.T) {
	source := &reportSource{timeout: true}
	s := Service{Source: source, Enabled: true, Admins: reportRoles{true}, Timeout: time.Millisecond}
	_, err := s.AdminReport(t.Context(), "admin", Range{})
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != "report_timeout" || app.Status != 503 {
		t.Fatalf("timeout: %v", err)
	}
	s.Enabled = false
	_, err = s.AdminReport(t.Context(), "admin", Range{})
	if !errors.As(err, &app) || app.Code != "feature_disabled" || source.calls != 1 {
		t.Fatalf("flag: %v calls=%d", err, source.calls)
	}
}

func TestReportShopRoutePathOverridesQueryAndDrillIsScoped(t *testing.T) {
	own := "00000000-0000-4000-8000-000000000001"
	other := "00000000-0000-4000-8000-000000000002"
	source := &reportSource{}
	s := Service{Source: source, Enabled: true, Shops: func(_ context.Context, actor, shop string) error {
		if actor != "owner" || shop != own {
			return apperror.Forbidden("not member")
		}
		return nil
	}}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, "owner") })
	h := Handlers{Service: s, Log: zerolog.Nop()}
	h.RegisterShop(r.Group("/api/payments/vendor/:vendorId"))
	for _, drill := range []bool{false, true} {
		for _, shop := range []string{own, other} {
			q := url.Values{"from": {"2026-10-01T00:00:00+07:00"}, "to": {"2026-10-02T00:00:00+07:00"}, "vendor_id": {other}, "metric": {"vendor_orders"}, "cutoff": {"2026-10-02T00:00:00Z"}}
			path := "/api/payments/vendor/" + shop + "/business-reports"
			if drill {
				path += "/drilldown"
			}
			before := source.calls
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path+"?"+q.Encode(), nil))
			if shop == own {
				if w.Code != 200 || source.vendor != own || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("own path: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 403 || source.calls != before {
				t.Fatalf("cross path: %d %s", w.Code, w.Body.String())
			}
		}
	}
}
