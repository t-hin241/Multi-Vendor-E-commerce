package transport

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"

	"github.com/rs/zerolog"
)

func TestInventoryMutationsRequireAuthentication(t *testing.T) {
	jwt := authjwttest.Manager()
	r := NewRouter("test", zerolog.Nop(), jwt, &ItemHandler{}, &InternalHandler{}, &AdminHandler{}, serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret"))
	for _, path := range []string{"/internal/inventory/reserve", "/internal/inventory/commit", "/internal/inventory/release", "/internal/inventory/returns"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{"order_id":"00000000-0000-0000-0000-000000000001"}`)))
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("internal mutation unprotected: %s %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/inventory/admin/restock-requests/00000000-0000-0000-0000-000000000001/approve", nil))
	if w.Code != 401 {
		t.Fatal("anonymous admin mutation accepted")
	}
}

func TestStockCountRequiresVendorAndValidInput(t *testing.T) {
	jwt := authjwttest.Manager()
	r := NewRouter("test", zerolog.Nop(), jwt, &ItemHandler{}, &InternalHandler{}, &AdminHandler{}, serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret"))
	const item = "/api/inventory/items/00000000-0000-0000-0000-000000000001/stock-counts"
	body := `{"count_id":"00000000-0000-0000-0000-000000000002","counted_on_hand":1,"reason":"damaged"}`

	serve := func(path, payload, role string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if role != "" {
			token, _, err := jwt.IssueAccessToken("00000000-0000-0000-0000-000000000009", role, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := serve(item, body, ""); code != 401 {
		t.Fatalf("anonymous stock count: %d", code)
	}
	for _, role := range []string{"buyer", "admin"} {
		if code := serve(item, body, role); code != 403 {
			t.Fatalf("%s must not record vendor stock counts: %d", role, code)
		}
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/inventory/items/not-a-uuid/stock-counts", body},
		{item, `{"count_id":"x","counted_on_hand":1,"reason":"damaged"}`},
		{item, `{"count_id":"00000000-0000-0000-0000-000000000002","counted_on_hand":-1,"reason":"damaged"}`},
		{item, `{"count_id":"00000000-0000-0000-0000-000000000002","reason":"damaged"}`},
		{item, `{"count_id":"00000000-0000-0000-0000-000000000002","counted_on_hand":1}`},
	} {
		if code := serve(tc.path, tc.body, "vendor"); code != 400 {
			t.Errorf("%s %s: expected 400, got %d", tc.path, tc.body, code)
		}
	}
}
