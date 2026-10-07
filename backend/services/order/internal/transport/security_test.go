package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
)

const someID = "00000000-0000-0000-0000-000000000001"

func testRouter() (http.Handler, *authjwt.Manager) {
	jwt := authjwttest.Manager()
	return NewRouter("test", zerolog.Nop(), jwt, &OrderHandler{}, &BuyerAddressHandler{}, &AdminHandler{}, &InternalHandler{}, &ReturnHandler{}, &SupportHandler{}, adminaccesstest.Guard(AdminRoutes),
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
		{"GET", "/internal/policy-rules/readiness?key=order.returns_window&value=window-7d"},
		{"POST", "/internal/policy-published"},
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
	// AF-17: a buyer account may be shop staff, so on seller routes the
	// shop permission (use case + Vendor) decides; an admin never acts here.
	if code := do(r, "POST", "/api/orders/vendor/return-requests/"+someID+"/receive", `{}`, as(t, jwt, "admin")); code != http.StatusForbidden {
		t.Fatalf("an admin must not receive returns as a shop: %d", code)
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

func TestSupportCaseRoutesCheckRolesAndInput(t *testing.T) {
	r, jwt := testRouter()
	forbidden := []struct {
		method, path, role string
	}{
		{"POST", "/api/orders/" + someID + "/support-cases", "vendor"},
		{"POST", "/api/orders/" + someID + "/support-cases", "admin"},
		{"POST", "/api/orders/support-cases/" + someID + "/reopen", "vendor"},
		{"POST", "/api/orders/vendor/support-cases/" + someID + "/messages", "admin"},
		{"GET", "/api/orders/admin/support-cases", "buyer"},
		{"GET", "/api/orders/admin/support-cases", "vendor"},
		{"POST", "/api/orders/admin/support-cases/" + someID + "/assignments", "vendor"},
		{"POST", "/api/orders/admin/support-cases/" + someID + "/resolutions", "buyer"},
		{"POST", "/api/orders/admin/support-cases/" + someID + "/messages", "buyer"},
		{"GET", "/api/orders/admin/support-cases/" + someID + "/attachments/" + someID, "vendor"},
	}
	for _, tc := range forbidden {
		if code := do(r, tc.method, tc.path, `{}`, as(t, jwt, tc.role)); code != http.StatusForbidden {
			t.Errorf("%s %s as %s: expected 403, got %d", tc.method, tc.path, tc.role, code)
		}
	}
	if code := do(r, "GET", "/api/orders/support-cases", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous case list: %d", code)
	}
	invalid := []struct {
		method, path, body, role string
	}{
		{"POST", "/api/orders/" + someID + "/support-cases", `{"vendor_order_id":"x","category":"other","message":"m"}`, "buyer"},
		{"POST", "/api/orders/" + someID + "/support-cases", `{"vendor_order_id":"` + someID + `","category":"other"}`, "buyer"},
		{"POST", "/api/orders/" + someID + "/support-cases", `{"vendor_order_id":"` + someID + `","category":"other","message":"m","attachment_ids":["a","b","c","d","e","f"]}`, "buyer"},
		{"GET", "/api/orders/support-cases/not-a-uuid", "", "buyer"},
		{"POST", "/api/orders/admin/support-cases/" + someID + "/assignments", `{"assignee_id":"` + someID + `"}`, "admin"},
		{"POST", "/api/orders/admin/support-cases/" + someID + "/resolutions", `{"resolution_kind":"refund","reason":"r","expected_version":1,"linked_operation_id":"x"}`, "admin"},
		{"GET", "/api/orders/admin/support-cases?assignee=someone", "", "admin"},
		{"POST", "/api/orders/support-attachments", "", "buyer"},
	}
	for _, tc := range invalid {
		if code := do(r, tc.method, tc.path, tc.body, as(t, jwt, tc.role)); code != http.StatusBadRequest {
			t.Errorf("%s %s %s: expected 400, got %d", tc.method, tc.path, tc.body, code)
		}
	}
}

func TestPolicyRoutesCheckRolesAndInput(t *testing.T) {
	r, jwt := testRouter()
	if code := do(r, "GET", "/api/orders/"+someID+"/policy-snapshot", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous snapshot read: %d", code)
	}
	if code := do(r, "GET", "/api/orders/not-a-uuid/policy-snapshot", "", as(t, jwt, "buyer")); code != http.StatusBadRequest {
		t.Fatalf("invalid id: %d", code)
	}
	key := map[string]string{serviceauth.Header: "fake-test-service-key-not-a-real-secret"}
	if code := do(r, "GET", "/internal/policy-rules/readiness?key=order.returns_window", "", key); code != http.StatusBadRequest {
		t.Fatalf("readiness needs key and value: %d", code)
	}
	if code := do(r, "POST", "/internal/policy-published", `{"policy_id":1}`, key); code != http.StatusBadRequest {
		t.Fatalf("malformed publication: %d", code)
	}
	if code := do(r, "POST", "/api/orders/checkout", `{"address_id":"`+someID+`","accepted_policy_versions":{"returns":"one"}}`, as(t, jwt, "buyer")); code != http.StatusBadRequest {
		t.Fatalf("accepted versions must be numbers: %d", code)
	}
}

// AF-19: every admin route names the permission bundle it needs.
func TestEveryAdminRouteNamesAPermission(t *testing.T) {
	r, _ := testRouter()
	adminaccesstest.AssertCovered(t, r.(*gin.Engine), AdminRoutes)
}
