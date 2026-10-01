package adapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/apperror"
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
	_, err := c.ListEligible(t.Context(), "00000000-0000-0000-0000-000000000001", "")
	return err
}

// Regression: Order answers in snake_case; the items must decode with
// their ids (they decoded empty before, so no review could be created).
func TestOrderClientDecodesEligibleItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"order_item_id":"item-1","vendor_order_id":"vo-1","product_id":"p-1","vendor_id":"v-1","product_name":"Áo","completed_at":"2026-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	items, err := NewHTTPOrderClient(server.URL, "fake-key").ListEligible(t.Context(), "buyer", "")
	if err != nil || len(items) != 1 || items[0].OrderItemID != "item-1" || items[0].VendorOrderID != "vo-1" || items[0].VendorID != "v-1" || items[0].ProductID != "p-1" {
		t.Fatalf("items = %+v, %v", items, err)
	}
}

func TestOrderOutageIsUnavailableNotForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := NewHTTPOrderClient(server.URL, "fake-key").ListEligible(t.Context(), "buyer", "")
	var app *apperror.Error
	if !errors.As(err, &app) || app.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %v", err)
	}
	server.Close()
	if _, err := NewHTTPOrderClient(server.URL, "fake-key").ListEligible(t.Context(), "buyer", ""); !errors.As(err, &app) || app.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Order is unreachable, got %v", err)
	}
}
