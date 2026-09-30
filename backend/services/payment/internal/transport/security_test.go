package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/payment/internal/usecase"
)

const (
	testServiceKey = "fake-test-service-key-not-a-real-secret"
	someID         = "00000000-0000-0000-0000-000000000001"
)

func send(r http.Handler, method, path, body string, headers map[string]string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func testRouter(jwt *authjwt.Manager) http.Handler {
	return NewRouter("test", zerolog.Nop(), jwt, Handlers{
		Payment: &PaymentHandler{}, Webhook: &WebhookHandler{},
		Refund: NewRefundHandler(usecase.NewRefundUseCase(nil, nil, zerolog.Nop()), zerolog.Nop()),
		Admin:  NewAdminHandler(usecase.NewReconciliationUseCase(usecase.ReconciliationDeps{}), usecase.NewSettlementUseCase(usecase.SettlementDeps{}), zerolog.Nop()),
	}, testServiceKey)
}

func TestRefundRoutesRequireServiceKeyOrAdmin(t *testing.T) {
	jwt := authjwt.NewManager("fake-test-signing-key-not-a-real-secret")
	r := testRouter(jwt)
	token := func(role string) map[string]string {
		tok, _, err := jwt.IssueAccessToken(someID, role, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + tok}
	}

	for _, headers := range []map[string]string{nil, {serviceauth.Header: "wrong"}, token("admin")} {
		if code := send(r, "POST", "/internal/payments/refunds", `{}`, headers); code != http.StatusForbidden {
			t.Errorf("refund intake must require the service key, got %d", code)
		}
	}
	if code := send(r, "POST", "/internal/payments/refunds", `{"order_refund_id":"x"}`, map[string]string{serviceauth.Header: testServiceKey}); code != http.StatusBadRequest {
		t.Errorf("invalid intake must be 400, got %d", code)
	}
	if code := send(r, "GET", "/api/payments/admin/refunds", "", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous admin list: %d", code)
	}
	for _, role := range []string{"buyer", "vendor"} {
		if code := send(r, "POST", "/api/payments/admin/refunds/"+someID+"/resolve", `{"outcome":"succeeded"}`, token(role)); code != http.StatusForbidden {
			t.Errorf("%s must not resolve refunds, got %d", role, code)
		}
	}
	if code := send(r, "POST", "/api/payments/admin/refunds/"+someID+"/resolve", `{"outcome":"refunded"}`, token("admin")); code != http.StatusBadRequest {
		t.Errorf("unknown outcome must be 400, got %d", code)
	}
	// Without an Identity verifier the use case fails closed.
	if code := send(r, "GET", "/api/payments/admin/refunds", "", token("admin")); code != http.StatusInternalServerError {
		t.Errorf("admin action without role re-verification must fail closed, got %d", code)
	}
}

func TestAdminAndInternalRoutesAreProtected(t *testing.T) {
	jwt := authjwt.NewManager("fake-test-signing-key-not-a-real-secret")
	r := testRouter(jwt)
	token := func(role string) map[string]string {
		tok, _, err := jwt.IssueAccessToken(someID, role, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + tok}
	}
	admin := []struct{ method, path, body string }{
		{"GET", "/api/payments/admin/reconciliation", ""},
		{"GET", "/api/payments/admin/search?q=abcdef", ""},
		{"POST", "/api/payments/admin/receipts/" + someID + "/retry", `{"reason":"x"}`},
		{"POST", "/api/payments/admin/intents/" + someID + "/reconcile", `{"reason":"x"}`},
		{"POST", "/api/payments/admin/order-sync/" + someID + "/retry", `{"reason":"x"}`},
		{"GET", "/api/payments/admin/settlements/balances", ""},
		{"POST", "/api/payments/admin/settlements/adjustments", `{"vendor_id":"` + someID + `","amount":1,"currency":"VND","reason":"x"}`},
		{"POST", "/api/payments/admin/payouts/batches", `{"idempotency_key":"fake-key-123","currency":"VND"}`},
		{"POST", "/api/payments/admin/payouts/items/" + someID + "/resolve", `{"outcome":"succeeded","evidence_reference":"x"}`},
	}
	for _, rt := range admin {
		if code := send(r, rt.method, rt.path, rt.body, nil); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d", rt.method, rt.path, code)
		}
		for _, role := range []string{"buyer", "vendor"} {
			if code := send(r, rt.method, rt.path, rt.body, token(role)); code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", role, rt.method, rt.path, code)
			}
		}
		// Without Identity re-verification the use cases fail closed.
		if code := send(r, rt.method, rt.path, rt.body, token("admin")); code != http.StatusInternalServerError {
			t.Errorf("admin %s %s must fail closed without role verification, got %d", rt.method, rt.path, code)
		}
	}
	for _, headers := range []map[string]string{nil, {serviceauth.Header: "wrong"}, token("admin")} {
		if code := send(r, "POST", "/internal/settlements/vendor-orders", `{}`, headers); code != http.StatusForbidden {
			t.Errorf("settlement intake must require the service key, got %d", code)
		}
	}
	if code := send(r, "POST", "/internal/settlements/vendor-orders", `{"vendor_order_id":"x"}`, map[string]string{serviceauth.Header: testServiceKey}); code != http.StatusBadRequest {
		t.Errorf("invalid settlement intake must be 400, got %d", code)
	}
	if code := send(r, "POST", "/api/payments/admin/payouts/items/"+someID+"/resolve", `{"outcome":"paid"}`, token("admin")); code != http.StatusBadRequest {
		t.Errorf("unknown payout outcome must be 400, got %d", code)
	}
}
