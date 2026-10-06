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

const someID = "00000000-0000-0000-0000-000000000001"

func testRouter() (http.Handler, *authjwt.Manager) {
	jwt := authjwttest.Manager()
	return NewRouter("test", zerolog.Nop(), jwt, &OrderHandler{}, &BuyerAddressHandler{}, &AdminHandler{}, &InternalHandler{}, &ReturnHandler{},
		serviceauth.SharedKey("fake-test-service-key-not-a-real-secret")), jwt
}

func do(r http.Handler, method, path, body string, headers map[string]string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func as(t *testing.T, jwt *authjwt.Manager, role string) map[string]string {
	t.Helper()
	token, _, err := jwt.IssueAccessToken(someID, role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestEveryInternalOrderRouteRequiresTheServiceKey(t *testing.T) {
	r, jwt := testRouter()
	routes := []struct{ method, path string }{
		{"GET", "/internal/orders/" + someID},
		{"POST", "/internal/orders/" + someID + "/mark-paid"},
		{"POST", "/internal/orders/" + someID + "/mark-payment-failed"},
		{"GET", "/internal/orders/" + someID + "/inventory-status"},
		{"GET", "/internal/orders/products/quantity-sold?product_ids=" + someID},
		{"GET", "/internal/orders/review-eligibility?buyer_id=" + someID},
		{"GET", "/internal/vendor-orders/" + someID},
		{"POST", "/internal/inventory-events"},
		{"POST", "/internal/refund-events"},
		{"POST", "/internal/settlements/holds"},
		{"POST", "/internal/shipment-events"},
	}
	for _, rt := range routes {
		for _, headers := range []map[string]string{nil, {serviceauth.Header: "wrong"}, as(t, jwt, "admin")} {
			if code := do(r, rt.method, rt.path, `{}`, headers); code != http.StatusForbidden {
				t.Errorf("%s %s must require the service key, got %d", rt.method, rt.path, code)
			}
		}
	}
}

func TestRolesAndInputValidation(t *testing.T) {
	r, jwt := testRouter()
	if code := do(r, "POST", "/api/orders/checkout", `{}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous checkout: %d", code)
	}
	if code := do(r, "POST", "/api/orders/admin/"+someID+"/refunds", `{}`, as(t, jwt, "buyer")); code != http.StatusForbidden {
		t.Fatalf("a buyer must not request admin refunds: %d", code)
	}
	if code := do(r, "POST", "/api/orders/vendor/return-requests/"+someID+"/receive", `{}`, as(t, jwt, "buyer")); code != http.StatusForbidden {
		t.Fatalf("a buyer must not receive returns: %d", code)
	}
	key := map[string]string{serviceauth.Header: "fake-test-service-key-not-a-real-secret"}
	cases := []struct {
		method, path, body string
		headers            map[string]string
	}{
		{"POST", "/api/orders/checkout", `{"address_id":"nope"}`, as(t, jwt, "buyer")},
		{"POST", "/api/orders/checkout", `{"address_id":"` + someID + `","expected_total_amount":0}`, as(t, jwt, "buyer")},
		{"POST", "/api/orders/checkout/preview", `{}`, as(t, jwt, "buyer")},
		{"GET", "/api/orders/not-a-uuid", "", as(t, jwt, "buyer")},
		{"POST", "/api/orders/" + someID + "/return-requests", `{"order_item_id":"x","reason":"r"}`, as(t, jwt, "buyer")},
		{"POST", "/api/orders/admin/" + someID + "/refunds", `{"reason_code":"refunded","amount":1,"reason":"x"}`, as(t, jwt, "admin")},
		{"POST", "/api/orders/admin/" + someID + "/transition", `{"status":"refunded","reason":"x"}`, as(t, jwt, "admin")},
		{"GET", "/api/orders/admin?from=yesterday", "", as(t, jwt, "admin")},
		{"POST", "/internal/orders/" + someID + "/mark-paid", `{"payment_id":"` + someID + `"}`, key},
		{"POST", "/internal/refund-events", `{"order_refund_id":"` + someID + `","status":"maybe"}`, key},
	}
	for _, tc := range cases {
		if code := do(r, tc.method, tc.path, tc.body, tc.headers); code != http.StatusBadRequest {
			t.Errorf("%s %s %s: expected 400, got %d", tc.method, tc.path, tc.body, code)
		}
	}
}
