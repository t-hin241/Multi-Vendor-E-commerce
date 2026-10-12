package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDelayForwardsRealUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/users/test" || r.Header.Get("X-Service-Name") != "vendor" {
			t.Error("proxy changed the authorization request")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	proxy := delayedProxy(upstream.URL, func() int64 { return 30 })
	r := httptest.NewRequest("GET", "/internal/users/test", nil)
	r.Header.Set("X-Service-Name", "vendor")
	w := httptest.NewRecorder()
	start := time.Now()
	proxy.ServeHTTP(w, r)
	if time.Since(start) < 30*time.Millisecond || w.Code != http.StatusForbidden {
		t.Fatal("fault delay or real upstream status was lost")
	}
}

func TestCancelledDelayDoesNotForward(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	proxy := delayedProxy(upstream.URL, func() int64 { return 10000 })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	proxy.ServeHTTP(httptest.NewRecorder(), r)
	if calls.Load() != 0 {
		t.Fatal("cancelled request reached upstream")
	}
}
