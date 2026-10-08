package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/shipment/internal/usecase"
)

const (
	testKey = "fake-test-service-key-not-a-real-secret"
	someID  = "00000000-0000-0000-0000-000000000001"
)

func TestShipmentRoutesAreProtected(t *testing.T) {
	jwt := authjwttest.Manager()
	uc := usecase.NewShipmentUseCase(usecase.Deps{})
	r := NewRouter("test", zerolog.Nop(), jwt, NewShipmentHandler(uc, zerolog.Nop()), &AdminHandler{}, &VendorShippingMethodHandler{},
		NewInternalHandler(uc, zerolog.Nop()), NewWebhookHandler(uc, zerolog.Nop()), NewOpsHandler(uc, zerolog.Nop()), adminaccesstest.Guard(AdminRoutes), serviceauth.SharedKey(testKey))
	// AF-19: every admin route names the permission bundle it needs.
	adminaccesstest.AssertCovered(t, r, AdminRoutes)
	send := func(method, path, body string, headers map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	token := func(role string) map[string]string {
		tok, _, err := jwt.IssueAccessToken(someID, role, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + tok}
	}
	for _, path := range []string{"/internal/shipments", "/internal/shipments/quotes", "/internal/shipments/by-vendor-order/" + someID + "/cancel", "/internal/shipments/by-vendor-order/" + someID + "/stops"} {
		for _, h := range []map[string]string{nil, {serviceauth.Header: "wrong"}, token("admin")} {
			if code := send("POST", path, `{}`, h); code != http.StatusForbidden {
				t.Errorf("%s must require the service key, got %d", path, code)
			}
		}
	}
	vendorActions := []string{"/ready", "/ship", "/tracking", "/failed-attempts", "/deliver", "/return", "/interception-decision"}
	for _, a := range vendorActions {
		path := "/api/shipments/" + someID + a
		if code := send("POST", path, `{}`, nil); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d", path, code)
		}
		// AF-17: a buyer account may be shop staff (the use case asks
		// Vendor); an admin account never acts for a shop here.
		if code := send("POST", path, `{}`, token("admin")); code != http.StatusForbidden {
			t.Errorf("an admin must not act on %s as a shop: %d", path, code)
		}
	}
	adminPaths := []struct{ method, path string }{
		{"GET", "/api/shipments/admin/operations"},
		{"POST", "/api/shipments/admin/shipments/" + someID + "/deliver"},
		{"POST", "/api/shipments/admin/shipments/" + someID + "/interception-decision"},
		{"POST", "/api/shipments/admin/order-events/" + someID + "/retry"},
	}
	for _, p := range adminPaths {
		if code := send(p.method, p.path, `{}`, token("vendor")); code != http.StatusForbidden {
			t.Errorf("a vendor must not use %s: %d", p.path, code)
		}
	}
	// Admin actions fail closed without Identity re-verification.
	if code := send("GET", "/api/shipments/admin/operations", "", token("admin")); code != http.StatusInternalServerError {
		t.Errorf("admin operations must fail closed without role verification, got %d", code)
	}
	if code := send("POST", "/api/shipments/"+someID+"/ship", `{}`, token("vendor")); code != http.StatusBadRequest {
		t.Errorf("shipping without a tracking number must be 400, got %d", code)
	}
}
