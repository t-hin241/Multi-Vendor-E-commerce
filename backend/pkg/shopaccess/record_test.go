package shopaccess

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/serviceauth"
)

// PW-021: the grant a request was authorized with is kept for its audit
// rows; a refusal records nothing; outside a request nothing is kept.
func TestRequestRemembersTheGrantItUsed(t *testing.T) {
	version := 3
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version < 0 {
			fmt.Fprint(w, `{"data":{"allowed":false,"vendor_id":"`+vendor+`"}}`)
			return
		}
		fmt.Fprintf(w, `{"data":{"allowed":true,"vendor_id":"%s","status":"approved","role":"staff","membership_version":%d}}`, vendor, version)
	}))
	defer s.Close()
	client := Client{URL: s.URL, Key: serviceauth.Credential("order", "test-key")}

	if UsedMembershipVersion(t.Context()) != nil {
		t.Fatal("no grant outside a request")
	}
	if _, err := client.Authorize(t.Context(), actor, vendor, OrdersFulfill); err != nil {
		t.Fatal(err)
	}
	if _, ok := UsedGrant(t.Context()); ok {
		t.Fatal("a context without a recorder keeps nothing")
	}

	ctx := WithGrants(t.Context())
	if _, err := client.Authorize(ctx, actor, vendor, OrdersFulfill); err != nil {
		t.Fatal(err)
	}
	if v := UsedMembershipVersion(ctx); v == nil || *v != 3 {
		t.Fatalf("version used: %v", v)
	}
	version = -1 // revoked: the refusal does not replace the grant used
	if _, err := client.Authorize(ctx, actor, vendor, OrdersFulfill); err == nil {
		t.Fatal("refusal expected")
	}
	if v := UsedMembershipVersion(ctx); *v != 3 {
		t.Fatalf("a refusal changed the recorded grant: %d", *v)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RecordGrants())
	var seen bool
	r.GET("/x", func(c *gin.Context) {
		_, seen = c.Request.Context().Value(grantsKey{}).(*grants)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if !seen {
		t.Fatal("the middleware must give the request a recorder")
	}
}
