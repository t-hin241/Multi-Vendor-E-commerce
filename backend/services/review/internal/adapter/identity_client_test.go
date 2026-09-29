package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPIdentityClientDisplayName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/users/buyer-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"full_name":"Nguyen Van A"}}`))
	}))
	defer server.Close()

	name, err := NewHTTPIdentityClient(server.URL, "synthetic-service-key").DisplayName(context.Background(), "buyer-1")
	if err != nil {
		t.Fatalf("DisplayName() error = %v", err)
	}
	if name != "Nguyen Van A" {
		t.Errorf("DisplayName() = %q, want %q", name, "Nguyen Van A")
	}
}

func TestHTTPIdentityClientDisplayNameReturnsEmptyForMissingUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	name, err := NewHTTPIdentityClient(server.URL, "synthetic-service-key").DisplayName(context.Background(), "missing")
	if err != nil {
		t.Fatalf("DisplayName() error = %v", err)
	}
	if name != "" {
		t.Errorf("DisplayName() = %q, want empty", name)
	}
}
