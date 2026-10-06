package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
)

const (
	testSigningKey = "fake-test-signing-key-not-a-real-secret"
	testServiceKey = "fake-test-service-key-not-a-real-secret"
	someUUID       = "00000000-0000-0000-0000-000000000001"
)

func newTestRouter() (http.Handler, *authjwt.Manager) {
	jwt := authjwttest.Manager()
	return NewRouter("test", zerolog.Nop(), jwt, &CartHandler{}, &InternalHandler{}, serviceauth.SharedKey(testServiceKey)), jwt
}

func serve(r http.Handler, method, path, body string, headers map[string]string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func bearer(t *testing.T, jwt *authjwt.Manager, role string) map[string]string {
	t.Helper()
	token, _, err := jwt.IssueAccessToken(someUUID, role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestInternalCheckoutContractRequiresServiceKey(t *testing.T) {
	r, jwt := newTestRouter()
	paths := []string{
		"/internal/carts/" + someUUID + "/checkout-snapshots",
		"/internal/carts/" + someUUID + "/checkout-snapshots/" + someUUID + "/consume",
	}
	for _, path := range paths {
		for _, headers := range []map[string]string{nil, {serviceauth.Header: "wrong-key"}, bearer(t, jwt, "buyer")} {
			if code := serve(r, "POST", path, `{}`, headers); code != http.StatusForbidden {
				t.Fatalf("%s must reject callers without the service key, got %d", path, code)
			}
		}
	}
}

func TestInternalCheckoutContractValidatesIDs(t *testing.T) {
	r, _ := newTestRouter()
	key := map[string]string{serviceauth.Header: testServiceKey}
	cases := []struct{ path, body string }{
		{"/internal/carts/not-a-uuid/checkout-snapshots", `{"operation_id":"` + someUUID + `"}`},
		{"/internal/carts/" + someUUID + "/checkout-snapshots", `{"operation_id":"nope"}`},
		{"/internal/carts/" + someUUID + "/checkout-snapshots/nope/consume", `{"lines":[{"line_id":"` + someUUID + `","quantity":1}]}`},
		{"/internal/carts/" + someUUID + "/checkout-snapshots/" + someUUID + "/consume", `{"lines":[]}`},
		{"/internal/carts/" + someUUID + "/checkout-snapshots/" + someUUID + "/consume", `{"lines":[{"line_id":"` + someUUID + `","quantity":0}]}`},
	}
	for _, tc := range cases {
		if code := serve(r, "POST", tc.path, tc.body, key); code != http.StatusBadRequest {
			t.Errorf("%s %s: expected 400, got %d", tc.path, tc.body, code)
		}
	}
}

func TestBuyerCartRoutesRequireBuyerRole(t *testing.T) {
	r, jwt := newTestRouter()
	if code := serve(r, "GET", "/api/cart", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous cart access: %d", code)
	}
	for _, role := range []string{"vendor", "admin"} {
		if code := serve(r, "POST", "/api/cart/items", `{}`, bearer(t, jwt, role)); code != http.StatusForbidden {
			t.Fatalf("%s must not use a buyer cart: %d", role, code)
		}
	}
}

func TestBuyerCartRoutesValidateInput(t *testing.T) {
	r, jwt := newTestRouter()
	auth := bearer(t, jwt, "buyer")
	cases := []struct{ method, path, body string }{
		{"GET", "/api/cart?limit=0", ""},
		{"GET", "/api/cart?limit=51", ""},
		{"GET", "/api/cart?offset=-1", ""},
		{"POST", "/api/cart/items", `{"product_id":"not-a-uuid","quantity":1}`},
		{"POST", "/api/cart/items", `{"product_id":"` + someUUID + `","variant_id":"x","quantity":1}`},
		{"POST", "/api/cart/items", `{"product_id":"` + someUUID + `","quantity":1,"expected_version":0}`},
		{"PATCH", "/api/cart/items/not-a-uuid", `{"quantity":1}`},
		{"PATCH", "/api/cart/items/" + someUUID, `{}`},
		{"PATCH", "/api/cart/items/" + someUUID + "?variant_id=bad", `{"quantity":1}`},
		{"DELETE", "/api/cart/items/" + someUUID + "?expected_version=abc", ""},
		{"DELETE", "/api/cart?expected_version=0", ""},
		{"POST", "/api/cart/price-confirmations", `{"expected_version":1,"lines":[]}`},
		{"POST", "/api/cart/price-confirmations", `{"expected_version":1,"lines":[{"line_id":"` + someUUID + `","price_amount":1,"currency":"VN"}]}`},
		{"POST", "/api/cart/items", `{"product_id":"` + someUUID + `","quantity":1,"pad":"` + strings.Repeat("x", 70<<10) + `"}`},
	}
	for _, tc := range cases {
		if code := serve(r, tc.method, tc.path, tc.body, auth); code != http.StatusBadRequest {
			t.Errorf("%s %s: expected 400, got %d", tc.method, tc.path, code)
		}
	}
}

func TestDescribeBindErrorNamesFields(t *testing.T) {
	r, jwt := newTestRouter()
	req := httptest.NewRequest("POST", "/api/cart/price-confirmations", strings.NewReader(`{"expected_version":1,"lines":[{"line_id":"x","price_amount":1,"currency":"VND"}]}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range bearer(t, jwt, "buyer") {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "lines[0].line_id must be a valid id") {
		t.Fatalf("expected a field-level message, got %s", rec.Body.String())
	}
}
