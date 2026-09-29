package identityclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoleVerificationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"active", `{"data":{"id":"test-user","role":"vendor","is_active":true}}`, 200, true},
		{"inactive", `{"data":{"id":"test-user","role":"vendor","is_active":false}}`, 200, false},
		{"missing active", `{"data":{"id":"test-user","role":"vendor"}}`, 200, false},
		{"wrong id", `{"data":{"id":"another-user","role":"vendor","is_active":true}}`, 200, false},
		{"wrong role", `{"data":{"id":"test-user","role":"buyer","is_active":true}}`, 200, false},
		{"outage", `{}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Identity-Service-Key") != "test-key" {
					t.Error("service key missing")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			err := (Client{URL: s.URL, Key: "test-key"}).RequireRole(t.Context(), "test-user", "vendor")
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected verification outcome %v", err)
			}
		})
	}
}
