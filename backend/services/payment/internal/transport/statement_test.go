package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

type statementTestAccess struct{ denied, unavailable bool }

func (a *statementTestAccess) Authorize(_ context.Context, _, vendor, permission string) (shopaccess.Grant, error) {
	if a.unavailable {
		return shopaccess.Grant{}, shopaccess.Unavailable(nil)
	}
	if a.denied || vendor != someID || permission != shopaccess.FinanceRead {
		return shopaccess.Grant{}, shopaccess.Denied("")
	}
	return shopaccess.Grant{}, nil
}

type statementTestStore struct {
	usecase.StatementStore
	reads int
}

func (s *statementTestStore) Read(_ context.Context, _ string, _ domain.StatementFilter, _ *domain.StatementCursor, _ int, _ repository.HoldLookup) (*domain.Statement, error) {
	s.reads++
	return &domain.Statement{Entries: []domain.StatementEntry{}}, nil
}

type statementTestAdmin struct{}

func (statementTestAdmin) Require(context.Context, string, string) (int64, error) {
	return 0, apperror.Forbidden("finance.read required")
}

func TestStatementHTTPShopPermissionAndRevocation(t *testing.T) {
	jwt := authjwttest.Manager()
	access := &statementTestAccess{}
	store := &statementTestStore{}
	r := statementTestRouter(jwt, &usecase.StatementUseCase{Enabled: true, Shops: access, Admins: statementTestAdmin{}, Store: store})
	path := "/api/payments/vendor/" + someID + "/statements?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&currency=VND"
	if code := send(r, "GET", path, "", nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	token, _, err := jwt.IssueAccessToken(someID, "buyer", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", headers["Authorization"])
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("buyer finance staff: %d %s", res.Code, res.Header().Get("Cache-Control"))
	}
	access.denied = true
	if code := send(r, "GET", path, "", headers); code != 403 {
		t.Fatalf("revoked: %d", code)
	}
	access.denied = false
	access.unavailable = true
	if code := send(r, "GET", path, "", headers); code != 503 {
		t.Fatalf("auth outage: %d", code)
	}
	access.unavailable = false
	if code := send(r, "GET", "/api/payments/vendor/00000000-0000-0000-0000-000000000002/statements?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&currency=VND", "", headers); code != 403 {
		t.Fatalf("cross shop: %d", code)
	}
	if store.reads != 1 {
		t.Fatalf("unauthorized read reached repository: %d", store.reads)
	}
	adminPath := "/api/payments/admin/vendor/" + someID + "/statements?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&currency=VND"
	if code := send(r, "GET", adminPath, "", headers); code != 403 {
		t.Fatalf("buyer admin route: %d", code)
	}
	admin, _, err := jwt.IssueAccessToken(someID, "admin", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code := send(r, "GET", adminPath, "", map[string]string{"Authorization": "Bearer " + admin}); code != 403 {
		t.Fatalf("admin without finance permission: %d", code)
	}
}
