package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/serviceauth"
)

// Order's internal routes require the service key; a client that forgets
// it silently loses its data.
func TestOrderClientSendsTheServiceKey(t *testing.T) {
	const key = "fake-test-service-key-not-a-real-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	if err := callOrder(t, NewHTTPOrderClient(server.URL, key)); err != nil {
		t.Fatalf("expected the keyed call to succeed: %v", err)
	}
	if err := callOrder(t, NewHTTPOrderClient(server.URL, "")); err == nil {
		t.Fatal("expected a call without the key to fail")
	}
}

func callOrder(t *testing.T, c *HTTPOrderClient) error {
	_, err := c.GetQuantitySold(t.Context(), []string{"00000000-0000-0000-0000-000000000001"})
	return err
}
