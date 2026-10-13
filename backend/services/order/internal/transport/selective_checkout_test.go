package transport

import (
	"net/http"
	"testing"
)

func TestSelectionValidationAndCapabilityAuthorization(t *testing.T) {
	r, jwt := testRouter()
	for _, path := range []string{"/api/orders/checkout", "/api/orders/checkout/preview"} {
		for _, selection := range []string{`null`, `[]`, `["bad"]`, `["` + someID + `","` + someID + `"]`} {
			body := `{"address_id":"` + someID + `","cart_version":1,"selected_line_ids":` + selection + `}`
			if status := do(r, "POST", path, body, as(t, jwt, "buyer")); status != 422 {
				t.Errorf("%s %s: got %d", path, selection, status)
			}
		}
	}
	for _, role := range []string{"vendor", "admin"} {
		if status := do(r, "GET", "/api/orders/checkout/capabilities", "", as(t, jwt, role)); status != http.StatusForbidden {
			t.Fatalf("capability role %s: %d", role, status)
		}
	}
	if status := do(r, "GET", "/api/orders/checkout/capabilities", "", nil); status != http.StatusUnauthorized {
		t.Fatal(status)
	}
}
