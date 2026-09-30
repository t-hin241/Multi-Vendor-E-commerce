package adapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/serviceauth"
)

const fakeKey = "fake-service-key-not-a-real-secret"

func TestDefaultDestinationSendsKeyAndMapsMissing(t *testing.T) {
	status, body := 200, `{"data":{"account_id":"acc-1","version":2,"bank_bin":"970400","last4":"1234"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != fakeKey || r.URL.Path != "/internal/payout-destinations/v-1/default" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	c := NewHTTPVendorClient(server.URL, fakeKey)
	d, err := c.DefaultDestination(t.Context(), "v-1")
	if err != nil || d.AccountID != "acc-1" || d.Version != 2 {
		t.Fatalf("unexpected %+v %v", d, err)
	}
	status = 404
	if _, err := c.DefaultDestination(t.Context(), "v-1"); !errors.Is(err, ErrNoPayoutDestination) {
		t.Fatalf("404 means no verified destination, got %v", err)
	}
	status, body = 200, `{"data":{"account_id":"acc-1","version":0}}`
	if _, err := c.DefaultDestination(t.Context(), "v-1"); err == nil {
		t.Fatal("an incomplete destination must be refused")
	}
}

func TestHeldVendorOrdersFailsClosed(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string][]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get(serviceauth.Header) != fakeKey || len(in["vendor_order_ids"]) != 2 {
			t.Error("unexpected request")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"held":[{"vendor_order_id":"vo-1","reason":"return_open"}]}}`))
	}))
	defer server.Close()
	c := NewHTTPOrderClient(server.URL, fakeKey)
	held, err := c.HeldVendorOrders(t.Context(), []string{"vo-1", "vo-2"})
	if err != nil || held["vo-1"] != "return_open" || len(held) != 1 {
		t.Fatalf("unexpected %v %v", held, err)
	}
	status = 503
	if _, err := c.HeldVendorOrders(t.Context(), []string{"vo-1", "vo-2"}); err == nil {
		t.Fatal("an unknown answer must be an error so nothing is paid")
	}
}
