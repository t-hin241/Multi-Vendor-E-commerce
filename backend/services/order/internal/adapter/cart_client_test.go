package adapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

func TestCartClientUsesServiceIdentityAndMapsErrors(t *testing.T) {
	const key = "fake-test-service-key-not-a-real-secret"
	status, body := http.StatusCreated, `{"data":{"operation_id":"op","cart_version":4,"lines":[{"line_id":"l1","product_id":"p1","quantity":2,"seen_price_amount":100,"seen_currency":"VND"}]}}`
	var gotPath string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != key || r.Header.Get("Authorization") != "" {
			t.Errorf("expected service key only, got headers %v", r.Header)
		}
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotPayload)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := NewHTTPCartClient(server.URL, key)

	version := int64(4)
	snap, err := client.Snapshot(t.Context(), "buyer-1", "op", &version)
	if err != nil || snap.CartVersion != 4 || len(snap.Lines) != 1 || snap.Lines[0].LineID != "l1" || *snap.Lines[0].SeenPriceAmount != 100 {
		t.Fatalf("unexpected snapshot %+v %v", snap, err)
	}
	if gotPath != "/internal/carts/buyer-1/checkout-snapshots" || gotPayload["expected_version"] != float64(4) {
		t.Fatalf("unexpected request %s %v", gotPath, gotPayload)
	}

	status, body = http.StatusConflict, `{"error":{"code":"cart_changed","message":"Your cart changed"}}`
	_, err = client.Snapshot(t.Context(), "buyer-1", "op", nil)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "cart_changed" || appErr.Status != http.StatusConflict {
		t.Fatalf("expected Cart's conflict to pass through, got %v", err)
	}

	for _, s := range []int{http.StatusForbidden, http.StatusBadGateway} {
		status, body = s, `{"error":{"code":"forbidden","message":"Service authentication required"}}`
		err = client.Consume(t.Context(), "buyer-1", "op", []CartConsumeLine{{LineID: "l1", Quantity: 2}})
		if !errors.As(err, &appErr) || appErr.Code != apperror.CodeInternal {
			t.Fatalf("status %d must be an internal (retryable, not buyer-facing) error, got %v", s, err)
		}
	}
	if gotPath != "/internal/carts/buyer-1/checkout-snapshots/op/consume" {
		t.Fatalf("unexpected consume path %s", gotPath)
	}
}
