package proxy_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/gateway/internal/proxy"
)

func TestProxyDoesNotTrustSpoofedClientIP(t *testing.T) {
	forwarded := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- r.Header.Get("X-Forwarded-For")
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	handler, err := proxy.NewReverseProxyHandler(upstream.URL, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	if err = router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	router.Any("/api/auth/login", handler)
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req = req.WithContext(t.Context())
	req.RemoteAddr = "192.0.2.12:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")
	req.Header.Set("X-Real-IP", "203.0.113.2")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != 204 {
		t.Fatalf("unexpected forwarding status: %d", recorder.Code)
	}
	if ip := <-forwarded; ip != "192.0.2.12" {
		t.Fatalf("unexpected forwarding IP: %s", ip)
	}
}
