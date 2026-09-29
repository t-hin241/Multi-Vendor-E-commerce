package adapter_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/services/notification/internal/adapter"
)

func TestResetSourceAuthenticatedReferenceContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reset-Delivery-Key") != "synthetic-delivery-key" || r.URL.Path != "/internal/password-reset-deliveries/test-id" {
			t.Error("invalid reference contract")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"buyer@example.invalid","url":"https://example.invalid/reset-password#token=synthetic"}`))
	}))
	defer server.Close()
	message, err := (adapter.ResetSource{URL: server.URL, Key: "synthetic-delivery-key"}).GetResetMessage(t.Context(), "test-id")
	if err != nil || message.URL == "" {
		t.Fatal("reset source failed")
	}
}
