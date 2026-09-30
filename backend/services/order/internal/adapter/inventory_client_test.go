package adapter

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"sync/atomic"
	"testing"
	"time"
)

func TestInventoryRetriesSameOperationAndRequiresReceipt(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000001"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) == "" {
			t.Error("missing authentication")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"order_id":%q,"operation_id":%q,"status":"held","expires_at":%q}}`, id, id, time.Now().Add(time.Minute).Format(time.RFC3339))
	}))
	defer server.Close()
	client := NewHTTPInventoryClient(server.URL, "fake-test-key-not-a-real-secret")
	if err := client.Reserve(t.Context(), id, []ReserveLine{{ProductID: id, Quantity: 1}}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("transient reserve not retried")
	}
	if err := client.Commit(t.Context(), id); err == nil {
		t.Fatal("held receipt accepted as committed")
	}
}
func TestInventoryKeepsBusinessErrorMessages(t *testing.T) {
	status := 409
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"Not enough stock available for product p1"}}`))
	}))
	defer server.Close()
	client := NewHTTPInventoryClient(server.URL, "fake-test-key-not-a-real-secret")

	err := client.Reserve(t.Context(), "order-1", []ReserveLine{{ProductID: "p1", Quantity: 9}})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeConflict || appErr.Message != "Not enough stock available for product p1" {
		t.Fatalf("expected Inventory's conflict message, got %v", err)
	}
	status = 400
	err = client.Reserve(t.Context(), "order-1", []ReserveLine{{ProductID: "p1", Quantity: 9}})
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodeValidation {
		t.Fatalf("expected a validation error, got %v", err)
	}
}

func TestInventoryRejectsEmptySuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":{"committed":true}}`)) }))
	defer server.Close()
	if err := NewHTTPInventoryClient(server.URL, "fake-test-key-not-a-real-secret").Commit(t.Context(), "test-order"); err == nil {
		t.Fatal("empty terminal success accepted")
	}
}
