package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/services/review/internal/domain"
)

// REV-03: the storefront view carries the masked label only: no buyer id,
// order item, shop internals or moderation details.
func TestPublicViewHidesBuyerData(t *testing.T) {
	label, note, reason := "N***n", "internal note", "reason-1"
	at := time.Now()
	r := &domain.Review{ID: "r-1", BuyerID: "buyer-uuid", VendorID: "shop-1", ProductID: "p-1", OrderItemID: "item-1", VendorOrderID: "vo-1",
		Rating: 5, Comment: "ok", Status: domain.ReviewPublished, VerifiedPurchase: true, AuthorLabel: &label,
		HiddenReasonID: &reason, HiddenNote: &note, HiddenAt: &at, CreatedAt: at}
	encode := func(v view) string {
		b, _ := json.Marshal(review(r, nil, nil, v))
		return string(b)
	}
	public := encode(publicView)
	for _, leak := range []string{"buyer-uuid", "buyer_id", "item-1", "vo-1", "shop-1", "internal note", "reason-1", "status"} {
		if strings.Contains(public, leak) {
			t.Errorf("public view leaks %q: %s", leak, public)
		}
	}
	if !strings.Contains(public, `"buyer_name":"N***n"`) || !strings.Contains(public, `"verified_purchase":true`) {
		t.Fatalf("public view: %s", public)
	}
	if owner := encode(ownerView); strings.Contains(owner, "buyer-uuid") || !strings.Contains(owner, "item-1") {
		t.Fatalf("owner view: %s", owner)
	}
	if admin := encode(adminView); !strings.Contains(admin, "buyer-uuid") || !strings.Contains(admin, "internal note") {
		t.Fatalf("admin view: %s", admin)
	}
}

type refuse struct{ err error }

func (r refuse) Allow(context.Context, string, int64, time.Duration) (bool, error) {
	return false, r.err
}

type allow struct{}

func (allow) Allow(context.Context, string, int64, time.Duration) (bool, error) {
	return true, errors.New("redis down")
}

func TestRoutesNeedTheRightRoleAndAreRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwt := authjwt.NewManager("test-signing-secret-not-a-real-secret-000")
	send := func(router http.Handler, method, path, token string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	buyer, _, _ := jwt.IssueAccessToken("11111111-1111-1111-1111-111111111111", "buyer", time.Minute)
	vendor, _, _ := jwt.IssueAccessToken("22222222-2222-2222-2222-222222222222", "vendor", time.Minute)
	router := NewRouter("test", zerolog.Nop(), jwt, NewHandler(nil, zerolog.Nop()), refuse{})
	id := "33333333-3333-3333-3333-333333333333"
	for _, route := range [][2]string{{"GET", "/api/reviews/admin"}, {"GET", "/api/reviews/admin/operations"}, {"POST", "/api/reviews/admin/" + id + "/hide"},
		{"POST", "/api/reviews/admin/" + id + "/restore"}, {"POST", "/api/reviews/admin/reports/" + id + "/resolve"}, {"POST", "/api/reviews/admin/moderation-reasons"}} {
		if code := send(router, route[0], route[1], ""); code != http.StatusUnauthorized {
			t.Errorf("%v without token: %d", route, code)
		}
		if code := send(router, route[0], route[1], buyer); code != http.StatusForbidden {
			t.Errorf("%v as buyer: %d", route, code)
		}
	}
	if code := send(router, "PUT", "/api/reviews/vendor/"+id+"/reply", buyer); code != http.StatusForbidden {
		t.Errorf("a buyer cannot reply as a shop: %d", code)
	}
	if code := send(router, "POST", "/api/reviews", vendor); code != http.StatusForbidden {
		t.Errorf("a shop cannot write a buyer review: %d", code)
	}
	for _, route := range [][3]string{{"POST", "/api/reviews", buyer}, {"POST", "/api/reviews/" + id + "/images", buyer},
		{"PUT", "/api/reviews/vendor/" + id + "/reply", vendor}, {"POST", "/api/reviews/vendor/" + id + "/reports", vendor}} {
		if code := send(router, route[0], route[1], route[2]); code != http.StatusTooManyRequests {
			t.Errorf("%v over the limit: %d", route[:2], code)
		}
	}
	// The counter store being down does not block reviews.
	open := NewRouter("test", zerolog.Nop(), jwt, NewHandler(nil, zerolog.Nop()), allow{})
	if code := send(open, "POST", "/api/reviews", buyer); code != http.StatusBadRequest {
		t.Errorf("with the limiter down the request reaches validation, got %d", code)
	}
}
