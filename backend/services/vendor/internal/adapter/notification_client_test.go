package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/vendorsvc/internal/repository"
)

func TestSendNoticeIdentifiesTheEventAndMapsAnswers(t *testing.T) {
	const key = "fake-test-service-key-not-a-real-secret"
	status := http.StatusAccepted
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer server.Close()
	n := repository.Notice{ID: "notice-1", VendorID: "shop-1", UserID: "user-1", Type: "vendor_approved"}
	if reason, _ := sendNotice(t.Context(), server.Client(), server.URL, key, n); reason != "" {
		t.Fatal(reason)
	}
	if got["event_id"] != "notice-1" || got["source"] != "vendor" || got["reference_id"] != "shop-1" {
		t.Fatalf("unexpected payload %v", got)
	}
	for code, refused := range map[int]bool{http.StatusBadRequest: true, http.StatusForbidden: false, http.StatusTooManyRequests: false, http.StatusServiceUnavailable: false} {
		status = code
		if reason, r := sendNotice(t.Context(), server.Client(), server.URL, key, n); reason == "" || r != refused {
			t.Fatalf("%d: reason %q refused %v", code, reason, r)
		}
	}
}
