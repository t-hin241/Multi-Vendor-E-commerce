package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/admin/internal/adapter"
	"shopee/backend/services/admin/internal/transport"
	"shopee/backend/services/admin/internal/usecase"
)

type allow struct{}

func (allow) RequireRole(context.Context, string, string) error { return nil }

type recorder struct{ queries []url.Values }

func (r *recorder) Get(_ context.Context, _, _ string, q url.Values, _ string) (json.RawMessage, error) {
	r.queries = append(r.queries, q)
	return json.RawMessage(`{"entries": []}`), nil
}

func TestAdminRoutesNeedAnAdminTokenAndOnlyRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwt.NewManager("test-signing-secret-not-a-real-secret-000")
	rec := &recorder{}
	svc := usecase.Service{Upstreams: []usecase.Upstream{{Name: "order", URL: "http://order"}}, Reader: rec, Roles: allow{}, Log: zerolog.Nop()}
	router := transport.NewRouter("test", zerolog.Nop(), jwt, svc)
	send := func(method, path, role string) int {
		req := httptest.NewRequest(method, path, nil)
		if role != "" {
			tok, _, _ := jwt.IssueAccessToken("11111111-1111-1111-1111-111111111111", role, time.Minute)
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	for _, path := range []string{"/api/admin/dashboard", "/api/admin/audit"} {
		if code := send("GET", path, ""); code != http.StatusUnauthorized {
			t.Fatalf("%s without token: %d", path, code)
		}
		for _, role := range []string{"buyer", "vendor"} {
			if code := send("GET", path, role); code != http.StatusForbidden {
				t.Fatalf("%s as %s: %d", path, role, code)
			}
		}
	}
	if code := send("POST", "/api/admin/audit", "admin"); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Fatalf("Admin exposes no mutation, got %d", code)
	}
	if code := send("GET", "/api/admin/audit?cursor_time=2026-01-01T00:00:00Z&cursor_id=~&action=refund_requested", "admin"); code != http.StatusOK {
		t.Fatalf("audit as admin: %d", code)
	}
	if q := rec.queries[len(rec.queries)-1]; q.Get("cursor_time") != "" || q.Get("action") != "refund_requested" {
		t.Fatalf("raw cursor parameters from the client must be dropped, filters kept: %v", q)
	}
	if code := send("GET", "/api/admin/audit?actor_id=not-a-uuid", "admin"); code != http.StatusBadRequest {
		t.Fatalf("invalid filter: %d", code)
	}
}

func TestUpstreamClientForwardsTheAdminAndMapsFailures(t *testing.T) {
	var gotAuth, gotRequest string
	status, body := http.StatusOK, `{"data": {"ok": true}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotRequest = r.Header.Get("Authorization"), r.Header.Get(middleware.RequestIDHeader)
		if r.Method != http.MethodGet {
			t.Errorf("only GET is sent, got %s", r.Method)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	client := adapter.Client{HTTP: srv.Client()}
	ctx := middleware.ContextWithRequestID(context.Background(), "req-admin-0001")

	data, err := client.Get(ctx, srv.URL, "/x", nil, "Bearer admin-token")
	if err != nil || string(data) != `{"ok": true}` || gotAuth != "Bearer admin-token" || gotRequest != "req-admin-0001" {
		t.Fatalf("forwarding: %s %v %q %q", data, err, gotAuth, gotRequest)
	}
	status, body = http.StatusForbidden, `{"error": {"code": "forbidden", "message": "no"}}`
	var refusal *adapter.Refusal
	if _, err := client.Get(ctx, srv.URL, "/x", nil, "Bearer admin-token"); !errors.As(err, &refusal) || refusal.Status != 403 {
		t.Fatalf("a 4xx is a refusal, got %v", err)
	}
	status, body = http.StatusInternalServerError, `{"error": {"code": "internal_error", "message": "boom"}}`
	if _, err := client.Get(ctx, srv.URL, "/x", nil, "Bearer admin-token"); !errors.Is(err, adapter.ErrUnavailable) {
		t.Fatalf("a 5xx is unavailable, got %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := client.Get(tctx, slow.URL, "/x", nil, "Bearer admin-token"); !errors.Is(err, adapter.ErrUnavailable) {
		t.Fatalf("a timeout is unavailable, got %v", err)
	}
}
