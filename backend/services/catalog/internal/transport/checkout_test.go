package transport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"

	"github.com/rs/zerolog"
)

type checkoutRepo struct{ usecase.ProductRepositoryPort }

func (checkoutRepo) ReadCheckout(context.Context, []string, []string) (*domain.CheckoutSnapshot, error) {
	return &domain.CheckoutSnapshot{Products: []domain.CheckoutProduct{
		{ID: "enforced", Status: domain.StatusApproved, IsActive: true, Version: 3, EnforcedVersion: 3},
		{ID: "synchronizing", Status: domain.StatusApproved, IsActive: true, Version: 4, EnforcedVersion: 3},
		{ID: "draft", Status: domain.StatusDraft, IsActive: true, Version: 3, EnforcedVersion: 3},
		{ID: "inactive", Status: domain.StatusApproved, Version: 3, EnforcedVersion: 3},
	}, Variants: []domain.CheckoutVariant{}}, nil
}

type checkoutVendors struct{ usecase.VendorGateway }

func (checkoutVendors) Approved(context.Context, []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func TestCheckoutSnapshotRequiresOrderAndPreservesVisibilityFence(t *testing.T) {
	uc := usecase.NewProductUseCase(checkoutRepo{}, nil, nil, nil, nil, checkoutVendors{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, usecase.Operations{})
	const key = "fake-checkout-test-key-not-a-real-secret"
	hash := sha256.Sum256([]byte(key))
	verifier := serviceauth.NewVerifier(map[string][][]byte{"order": {hash[:]}, "cart": {hash[:]}}, "")
	r := NewRouter("test", zerolog.Nop(), authjwt.NewManager("fake-jwt-key-not-a-real-secret"), &CategoryHandler{}, &ProductHandler{}, &StorefrontHandler{}, &AdminHandler{}, NewInternalHandler(uc, zerolog.Nop()), &AttributeHandler{}, verifier)
	for _, caller := range []string{"", "cart", "order"} {
		req := httptest.NewRequest(http.MethodPost, "/internal/products/checkout-snapshot", strings.NewReader(`{"product_ids":["00000000-0000-0000-0000-000000000001"],"variant_ids":[]}`))
		req.Header.Set("Content-Type", "application/json")
		if caller != "" {
			req.Header.Set(serviceauth.CallerHeader, caller)
			req.Header.Set(serviceauth.Header, key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if caller != "order" {
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("caller %q allowed: %d", caller, w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("order denied: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Data struct {
				Products []internalProductResponse `json:"products"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data.Products) != 4 {
			t.Fatal("missing products")
		}
		for _, p := range body.Data.Products {
			if p.IsVisible != (p.ID == "enforced") {
				t.Fatalf("visibility fence bypassed: %+v", p)
			}
		}
	}
}
