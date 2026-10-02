package transport

import (
	"net/http/httptest"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/serviceauth"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestInternalCatalogRequiresServiceAuthentication(t *testing.T) {
	r := NewRouter("test", zerolog.Nop(), authjwt.NewManager("fake-test-signing-key-not-a-real-secret"), &CategoryHandler{}, &ProductHandler{}, &StorefrontHandler{}, &AdminHandler{}, &InternalHandler{}, &AttributeHandler{}, serviceauth.SharedKey("fake-test-service-key-not-a-real-secret"))
	for _, path := range []string{"/internal/products/00000000-0000-0000-0000-000000000001", "/internal/products/variants/00000000-0000-0000-0000-000000000002"} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 401 && response.Code != 403 {
			t.Fatalf("unprotected internal endpoint: %d", response.Code)
		}
	}
	for _, path := range []string{"/api/catalog/products?q=" + strings.Repeat("a", 201), "/api/catalog/products?sort=unsafe", "/api/catalog/products?category_id=invalid"} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 400 {
			t.Fatalf("invalid query not rejected: %d", response.Code)
		}
	}
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PATCH", "/api/catalog/products/00000000-0000-0000-0000-000000000001/active", nil))
	if response.Code != 401 {
		t.Fatalf("unprotected vendor mutation: %d", response.Code)
	}
}
