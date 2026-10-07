package shopaccess

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

const (
	actor  = "11111111-1111-4111-8111-111111111111"
	vendor = "22222222-2222-4222-8222-222222222222"
)

func errCode(err error) apperror.Code {
	if app, ok := err.(*apperror.Error); ok {
		return app.Code
	}
	return ""
}

func TestAuthorizeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   apperror.Code
	}{
		{"allowed", 200, `{"data":{"allowed":true,"vendor_id":"` + vendor + `","status":"approved","role":"staff","membership_version":3}}`, ""},
		{"refused", 200, `{"data":{"allowed":false,"vendor_id":"` + vendor + `"}}`, CodePermissionDenied},
		{"another shop", 200, `{"data":{"allowed":true,"vendor_id":"` + actor + `","status":"approved"}}`, CodeAuthorizationUnavailable},
		{"unreadable", 200, `not json`, CodeAuthorizationUnavailable},
		{"malformed question", 400, `{}`, CodePermissionDenied},
		{"outage", 503, `{}`, CodeAuthorizationUnavailable},
		{"server error", 500, `{}`, CodeAuthorizationUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in map[string]string
				_ = json.NewDecoder(r.Body).Decode(&in)
				if r.URL.Path != "/internal/vendors/authorize" || r.Header.Get(serviceauth.CallerHeader) != "order" ||
					in["actor_user_id"] != actor || in["vendor_id"] != vendor || in["permission"] != OrdersFulfill {
					t.Errorf("unexpected request %s %v", r.URL.Path, in)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			g, err := (Client{URL: s.URL, Key: serviceauth.Credential("order", "test-key")}).Authorize(t.Context(), actor, vendor, OrdersFulfill)
			if errCode(err) != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if tc.want == "" && (g.Role != "staff" || g.MembershipVersion != 3 || g.Status != "approved") {
				t.Fatalf("grant not carried: %+v", g)
			}
		})
	}
}

func TestAuthorizeRefusesWithoutCallingOnBadInput(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	c := Client{URL: s.URL, Key: "test-key"}
	for _, args := range [][3]string{{"", vendor, OrdersRead}, {actor, "not-a-uuid", OrdersRead}, {actor, vendor, "orders.delete"}} {
		if _, err := c.Authorize(t.Context(), args[0], args[1], args[2]); errCode(err) != CodePermissionDenied {
			t.Fatalf("bad input %v not refused: %v", args, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("bad input reached Vendor")
	}
}

// An older Vendor without the contract answers 404: the client falls back
// to the owner-only lookup, which never grants staff.
func TestAuthorizeFallsBackToOwnerOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner int
		want  apperror.Code
	}{{"owner", 200, ""}, {"not owner", 403, CodePermissionDenied}, {"down", 502, CodeAuthorizationUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/vendors/authorize" {
					http.NotFound(w, r)
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/"+vendor+"/owned-by/"+actor) {
					t.Errorf("unexpected fallback path %s", r.URL.Path)
				}
				w.WriteHeader(tc.owner)
				fmt.Fprint(w, `{"data":{"vendor_id":"`+vendor+`","status":"suspended","version":4}}`)
			}))
			defer s.Close()
			g, err := (Client{URL: s.URL, Key: "test-key"}).Authorize(t.Context(), actor, vendor, PayoutDestinationWrite)
			if errCode(err) != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if tc.want == "" && (g.Role != "owner" || g.Status != "suspended" || g.VendorVersion != 4) {
				t.Fatalf("owner grant not carried: %+v", g)
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	if Grantable(PayoutDestinationWrite) || Grantable(ShopSettingsWrite) || Grantable(MarketingManage) || !Grantable(OrdersFulfill) {
		t.Fatal("registry grantability wrong")
	}
	if Known("orders.delete") || !Known(LiveHost) {
		t.Fatal("registry membership wrong")
	}
	if len(Registry()) != 16 {
		t.Fatal("registry v1 has 14 staff permissions and 2 owner-only ones")
	}
}
