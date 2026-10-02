package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/middleware"
)

func serve(t *testing.T, upstream string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := NewReverseProxyHandler(upstream, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Any("/api/orders/*path", handler)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(t.Context()))
	return rec
}

// Service credentials a client sends are dropped before the service sees
// the request.
func TestInternalCredentialHeadersAreDropped(t *testing.T) {
	got := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	req := httptest.NewRequest(http.MethodGet, "/api/orders/mine", nil)
	for _, h := range internalCredentialHeaders {
		req.Header.Set(h, "forged-by-client")
	}
	req.Header.Set("Authorization", "Bearer user-token")
	if rec := serve(t, upstream.URL, req); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	h := <-got
	for _, name := range internalCredentialHeaders {
		if h.Get(name) != "" {
			t.Errorf("%s reached the service", name)
		}
	}
	if h.Get("Authorization") != "Bearer user-token" {
		t.Error("the user's own credentials must still be forwarded")
	}
}

// A service that does not answer is a 504 with a request id, not a hung
// client.
func TestSlowServiceIsAGatewayTimeout(t *testing.T) {
	previous := upstreamTransport.ResponseHeaderTimeout
	upstreamTransport.ResponseHeaderTimeout = 200 * time.Millisecond
	t.Cleanup(func() { upstreamTransport.ResponseHeaderTimeout = previous })
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	defer close(release)

	started := time.Now()
	rec := serve(t, upstream.URL, httptest.NewRequest(http.MethodGet, "/api/orders/mine", nil))
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusGatewayTimeout || body.Error.Code != "upstream_timeout" || body.Error.RequestID == "" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the client waited far beyond the upstream timeout")
	}
}
