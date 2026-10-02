package serviceauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const (
	orderKey   = "fake-order-key-not-a-real-secret-000000"
	paymentKey = "fake-payment-key-not-a-real-secret-0000"
	rotatedKey = "fake-payment-key-rotated-not-real-00000"
	sharedKey  = "fake-shared-key-not-a-real-secret-00000"
)

func registry(t *testing.T) map[string][][]byte {
	t.Helper()
	r, err := ParseRegistry("order=" + HashKey(orderKey) + ",payment=" + HashKey(paymentKey) + "|" + HashKey(rotatedKey))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func call(v *Verifier, credential string, callers ...string) int {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/internal/x", v.Allow(callers...), func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/internal/x", strings.NewReader("{}"))
	if credential != "" {
		SetRequestHeaders(req, credential)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestOnlyNamedServicesPass(t *testing.T) {
	v := NewVerifier(registry(t), "")
	cases := []struct {
		name, credential string
		callers          []string
		want             int
	}{
		{"allowed caller", Credential("payment", paymentKey), []string{"payment"}, 200},
		{"rotated key", Credential("payment", rotatedKey), []string{"payment"}, 200},
		{"other service", Credential("order", orderKey), []string{"payment"}, 403},
		{"impersonation with own key", Credential("payment", orderKey), []string{"payment"}, 403},
		{"unknown service", Credential("gateway", orderKey), nil, 403},
		{"no credential", "", nil, 403},
		{"shared key not accepted", sharedKey, nil, 403},
		{"any registered service", Credential("order", orderKey), nil, 200},
	}
	for _, c := range cases {
		if got := call(v, c.credential, c.callers...); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSharedKeyOnlyDuringRollout(t *testing.T) {
	v := NewVerifier(registry(t), sharedKey)
	if call(v, sharedKey, "payment") != 200 {
		t.Fatal("the shared key passes while the rollout flag is on")
	}
	if call(v, Credential("order", orderKey), "payment") != 403 {
		t.Fatal("a named caller is still held to the route's list")
	}
	if call(SharedKey(sharedKey), "wrong-key-not-a-real-secret-0000000000") != 403 {
		t.Fatal("a wrong shared key is refused")
	}
}

func TestRegistryAndCredentialFormat(t *testing.T) {
	for _, bad := range []string{"order", "Order=" + HashKey(orderKey), "order=zz", "order=" + HashKey(orderKey)[:10]} {
		if _, err := ParseRegistry(bad); err == nil {
			t.Errorf("registry %q must be refused", bad)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	SetRequestHeaders(req, Credential("order", orderKey))
	if req.Header.Get(CallerHeader) != "order" || req.Header.Get(Header) != orderKey {
		t.Fatal("named credential headers")
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	SetRequestHeaders(req, sharedKey)
	if req.Header.Get(CallerHeader) != "" || req.Header.Get(Header) != sharedKey {
		t.Fatal("legacy credential headers")
	}
}
