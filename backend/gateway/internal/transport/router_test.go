package transport_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/gateway/internal/config"
	"shopee/backend/gateway/internal/transport"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestRouter_ProxiesBarePrefixWithoutRedirecting guards against a real bug:
// a route registered only as "prefix/*wildcard" doesn't match the bare
// prefix (e.g. GET /api/cart with nothing after it), which sends Gin into
// a trailing-slash redirect that a reverse-proxied client bounces on
// forever instead of ever reaching the upstream service.
//
// This has to run over a real net.Listener (httptest.NewServer), not
// httptest.NewRecorder: httputil.ReverseProxy expects the ResponseWriter to
// support http.CloseNotifier for request-cancellation plumbing, which a
// bare ResponseRecorder doesn't implement and a real connection always
// does.
func TestRouter_ProxiesBarePrefixWithoutRedirecting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Env:            "test",
		AllowedOrigins: []string{"http://localhost:3000"},
		Upstreams:      map[string]string{"/api/cart": upstream.URL},
	}

	router, err := transport.NewRouter(cfg, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected error building router: %v", err)
	}

	gateway := httptest.NewServer(router)
	defer gateway.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // don't auto-follow; a redirect at all is the failure mode we're checking for
		},
	}

	resp, err := client.Get(gateway.URL + "/api/cart")
	if err != nil {
		t.Fatalf("unexpected error calling the gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		t.Fatalf("expected the bare prefix to reach the upstream directly, got a redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from the upstream, got %d", resp.StatusCode)
	}
}

// TestRouter_LongerPrefixWinsOverAShorterOverlappingOne guards the
// /api/webhooks + /api/webhooks/shipment-carrier overlap: both are real
// prefixes in the routing table (Payment's general webhook endpoint vs
// Shipment's carrier-specific one nested under it), and only the sort-by-
// length-then-first-match in NewRouter keeps the more specific one from
// being shadowed by the shorter one.
func TestRouter_LongerPrefixWinsOverAShorterOverlappingOne(t *testing.T) {
	general := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"upstream":"general"}}`))
	}))
	defer general.Close()

	specific := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"upstream":"specific"}}`))
	}))
	defer specific.Close()

	cfg := config.Config{
		Env:            "test",
		AllowedOrigins: []string{"http://localhost:3000"},
		Upstreams: map[string]string{
			"/api/webhooks":                  general.URL,
			"/api/webhooks/shipment-carrier": specific.URL,
		},
	}

	router, err := transport.NewRouter(cfg, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected error building router: %v", err)
	}

	gateway := httptest.NewServer(router)
	defer gateway.Close()

	resp, err := http.Post(gateway.URL+"/api/webhooks/shipment-carrier", "application/json", nil)
	if err != nil {
		t.Fatalf("unexpected error calling the gateway: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), `"upstream":"specific"`) {
		t.Errorf("expected the more specific prefix's upstream to handle the request, got: %s", body)
	}
}
