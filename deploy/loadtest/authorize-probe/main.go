// Test-only probe for PW-020. Never deploy this service outside an isolated
// load-test network: it deliberately exposes fault controls and a service caller.
package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
)

type fault struct {
	IdentityMS int64 `json:"identity_ms"`
	VendorMS   int64 `json:"vendor_ms"`
}

func delayedProxy(target string, delay func() int64) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatal("invalid test upstream")
	}
	p := httputil.NewSingleHostReverseProxy(u)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 256
	transport.MaxIdleConnsPerHost = 32
	p.Transport = transport
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "test upstream unavailable", http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(time.Duration(delay()) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		p.ServeHTTP(w, r)
	})
}

func main() {
	// Configuration is read once here; no request or credential is logged.
	key := os.Getenv("CATALOG_INTERNAL_KEY")
	if key == "" {
		log.Fatal("CATALOG_INTERNAL_KEY is required")
	}
	var faults atomic.Pointer[fault]
	faults.Store(&fault{})
	mux := http.NewServeMux()
	mux.Handle("/identity/", http.StripPrefix("/identity", delayedProxy("http://identity:8081", func() int64 { return faults.Load().IdentityMS })))
	mux.Handle("/vendor/", http.StripPrefix("/vendor", delayedProxy("http://vendor:8082", func() int64 { return faults.Load().VendorMS })))
	mux.HandleFunc("POST /fault", func(w http.ResponseWriter, r *http.Request) {
		var f fault
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&f) != nil || f.IdentityMS < 0 || f.IdentityMS > 10000 || f.VendorMS < 0 || f.VendorMS > 10000 {
			http.Error(w, "invalid fault", 400)
			return
		}
		faults.Store(&f)
		w.WriteHeader(http.StatusNoContent)
	})
	client := shopaccess.Client{URL: "http://localhost:8080/vendor", Key: serviceauth.Credential("catalog", key)}
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Actor      string `json:"actor_user_id"`
			Vendor     string `json:"vendor_id"`
			Permission string `json:"permission"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
			http.Error(w, "invalid input", 400)
			return
		}
		ctx := middleware.ContextWithRequestID(r.Context(), r.Header.Get("X-Request-ID"))
		grant, err := client.Authorize(ctx, input.Actor, input.Vendor, input.Permission)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			var app *apperror.Error
			if !errors.As(err, &app) {
				http.Error(w, "unexpected probe error", 500)
				return
			}
			w.WriteHeader(app.Status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": app.Code}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"allowed": true, "role": grant.Role, "membership_version": grant.MembershipVersion}})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	srv := http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
