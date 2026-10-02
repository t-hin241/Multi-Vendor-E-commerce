package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/gateway/internal/config"
)

func gatewayFor(t *testing.T, upstream string, trustedProxies ...string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router, err := NewRouter(config.Config{Env: "test", AllowedOrigins: []string{"http://localhost:3000"}, TrustedProxies: trustedProxies,
		Upstreams: map[string]string{"/api/orders": upstream, "/api/auth": upstream}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// PLT-01: the public entry point never reaches an internal route, also
// through "..", encoded slashes or case tricks.
func TestInternalRoutesAreNotReachable(t *testing.T) {
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	gw := gatewayFor(t, upstream.URL)
	for _, path := range []string{
		"/internal/orders/x", "/api/orders/../internal/orders/x", "/api/orders/%2e%2e/internal/x",
		"/api/orders/%2E%2E%2Finternal", "/api/orders/x%2Finternal", "/api/orders/INTERNAL/x", "/api/orders/..%5cinternal",
	} {
		req, _ := http.NewRequest(http.MethodGet, gw.URL+path, nil)
		req.URL.Opaque = path // send as written, without client-side cleaning
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %d", path, resp.StatusCode)
		}
	}
	if reached.Load() != 0 {
		t.Fatalf("an internal path reached a service %d times", reached.Load())
	}
}

// PLT-02: login is limited per client IP with a request id in the answer;
// other traffic keeps its own budget.
func TestRateLimitPerClientAndKind(t *testing.T) {
	l := newRateLimiter()
	now := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 30; i++ {
		if ok, _ := l.allow("auth|1.1.1.1", 30); !ok {
			t.Fatalf("request %d refused early", i+1)
		}
	}
	if ok, retry := l.allow("auth|1.1.1.1", 30); ok || retry <= 0 {
		t.Fatal("the 31st login in a minute is refused with a retry delay")
	}
	if ok, _ := l.allow("auth|2.2.2.2", 30); !ok {
		t.Fatal("another client has its own budget")
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.allow("auth|1.1.1.1", 30); !ok {
		t.Fatal("the budget resets after the window")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	gw := gatewayFor(t, upstream.URL)
	var last *http.Response
	for i := 0; i < 31; i++ {
		resp, err := http.Post(gw.URL+"/api/auth/login", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		last = resp
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" || last.Header.Get("X-Request-ID") == "" {
		t.Fatalf("expected 429 with Retry-After and a request id, got %d", last.StatusCode)
	}
	resp, err := http.Get(gw.URL + "/api/orders/mine")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("other requests are not affected by the login budget")
	}
	resp.Body.Close()
}

// PLT-02/04: oversized bodies are refused at the edge; a stuck service is
// a 504 with a request id, not a hung client.
func TestBodyCapAndUpstreamErrors(t *testing.T) {
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	gw := gatewayFor(t, upstream.URL)
	resp, err := http.Post(gw.URL+"/api/orders/checkout", "application/json", strings.NewReader(strings.Repeat("x", maxBody+10)))
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized body: got %d", resp.StatusCode)
		}
	}
	if reached.Load() != 0 {
		t.Fatal("an oversized body must not reach the service")
	}
	upstream.Close() // service down
	resp, err = http.Get(gw.URL + "/api/orders/mine")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusBadGateway || body.Error.RequestID == "" {
		t.Fatalf("expected 502 with a request id, got %d %+v", resp.StatusCode, body)
	}
}

func loginFrom(t *testing.T, gw, forwardedFor string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, gw+"/api/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", forwardedFor)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Behind the TLS proxy every request arrives from the proxy's address: the
// budget follows the visitor's IP the proxy forwards, so one visitor cannot
// exhaust everyone's logins. A forwarded IP from anyone else is ignored, so
// a client cannot dodge the limit by inventing addresses.
func TestRateLimitFollowsForwardedClientOnlyFromTrustedProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()

	behindProxy := gatewayFor(t, upstream.URL, "127.0.0.1")
	for i := 0; i < 30; i++ {
		loginFrom(t, behindProxy.URL, "203.0.113.7")
	}
	if code := loginFrom(t, behindProxy.URL, "203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("the 31st login of one visitor: got %d", code)
	}
	if code := loginFrom(t, behindProxy.URL, "198.51.100.9"); code != http.StatusOK {
		t.Fatalf("another visitor behind the same proxy keeps its budget: got %d", code)
	}

	direct := gatewayFor(t, upstream.URL) // trusts no proxy
	for i := 0; i < 30; i++ {
		loginFrom(t, direct.URL, "198.51.100."+strconv.Itoa(i))
	}
	if code := loginFrom(t, direct.URL, "198.51.100.200"); code != http.StatusTooManyRequests {
		t.Fatalf("forged X-Forwarded-For must not reset the budget: got %d", code)
	}
}

// A product video (up to 20MB, multipart) reaches Catalog; anything else
// stays small. Oversized bodies of either kind stop at the gateway.
func TestBodyCapDependsOnKind(t *testing.T) {
	var received atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		received.Store(n)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	gw := gatewayFor(t, upstream.URL)
	post := func(contentType string, size int) int {
		t.Helper()
		resp, err := http.Post(gw.URL+"/api/orders/x", contentType, bytes.NewReader(make([]byte, size)))
		if err != nil {
			return -1 // the gateway may close the connection while the body is still being sent
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	video := 20 << 20
	if code := post("multipart/form-data; boundary=x", video); code != http.StatusOK || received.Load() != int64(video) {
		t.Fatalf("a 20MB upload: status %d, upstream got %d bytes", code, received.Load())
	}
	received.Store(0)
	for _, tc := range []struct {
		contentType string
		size        int
	}{{"multipart/form-data; boundary=x", maxUploadBody + 1}, {"application/json", maxBody + 1}} {
		if code := post(tc.contentType, tc.size); code != http.StatusRequestEntityTooLarge && code != -1 {
			t.Errorf("%s of %d bytes: got %d", tc.contentType, tc.size, code)
		}
	}
	if received.Load() != 0 {
		t.Fatal("an oversized body reached the service")
	}
}
