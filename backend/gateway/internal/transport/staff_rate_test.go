package transport

import (
	"net/http"
	"testing"
)

func firstRule(method, path, contentType string) string {
	for _, r := range rateRules {
		if r.match(method, path, contentType) {
			return r.name
		}
	}
	return ""
}

func TestStaffInvitationRequestsHaveTheirOwnBudget(t *testing.T) {
	for _, p := range []string{"/api/vendor/00000000-0000-4000-8000-000000000001/staff-invitations", "/api/vendor/staff-invitations/accept"} {
		if got := firstRule(http.MethodPost, p, "application/json"); got != "staff" {
			t.Fatalf("%s uses %q", p, got)
		}
	}
	if got := firstRule(http.MethodGet, "/api/vendor/accessible-shops", ""); got != "default" {
		t.Fatalf("reads must stay on the default budget, got %q", got)
	}
}
