package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

type checkoutVendorGateway struct {
	*fakeVendorGateway
	calls  [][]string
	denied string
	outage bool
}

func (v *checkoutVendorGateway) Approved(_ context.Context, ids []string) (map[string]int64, error) {
	v.calls = append(v.calls, append([]string(nil), ids...))
	if v.outage {
		return nil, apperror.Internal(errors.New("test vendor outage"))
	}
	for _, id := range ids {
		if id == v.denied {
			return nil, apperror.Conflict("shop unavailable")
		}
	}
	out := map[string]int64{}
	for _, id := range ids {
		out[id] = 1
	}
	return out, nil
}

func TestCheckoutCatalogBatchesLiveVendorChecksAndFailsClosed(t *testing.T) {
	p1, p2, p3 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	v := &checkoutVendorGateway{fakeVendorGateway: newFakeVendorGateway()}
	r := newFakeProductRepository()
	r.checkout = &domain.CheckoutSnapshot{Products: []domain.CheckoutProduct{
		{ID: p1, VendorID: "a", Status: domain.StatusApproved, IsActive: true},
		{ID: p2, VendorID: "a", Status: domain.StatusApproved, IsActive: true},
		{ID: p3, VendorID: "b", Status: domain.StatusApproved, IsActive: true},
	}}
	uc := usecase.NewProductUseCase(r, nil, nil, nil, nil, v, nil, nil, nil, nil, nil, nil, nil, nil, nil, usecase.Operations{})
	s, err := uc.ReadCheckout(t.Context(), []string{p1, p2, p3}, nil)
	if err != nil || len(s.Products) != 3 || len(v.calls) != 1 || len(v.calls[0]) != 2 {
		t.Fatalf("not batched: %+v %v", v.calls, err)
	}
	v.calls = nil
	v.denied = "b"
	s, err = uc.ReadCheckout(t.Context(), []string{p1, p2, p3}, nil)
	if err != nil || !s.Products[0].IsActive || !s.Products[1].IsActive || s.Products[2].IsActive || len(v.calls) != 3 {
		t.Fatalf("wrong denial: %+v %v", s, err)
	}
	v.outage = true
	if _, err = uc.ReadCheckout(t.Context(), []string{p1, p2}, nil); err == nil {
		t.Fatal("vendor outage permitted checkout")
	}
}

func TestCheckoutCatalogBoundsAndValidatesInputsBeforeDependencies(t *testing.T) {
	uc := usecase.NewProductUseCase(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, usecase.Operations{})
	id := uuid.NewString()
	for _, tc := range []struct{ p, v []string }{{nil, nil}, {[]string{"invalid"}, nil}, {make([]string, 51), nil}, {[]string{id}, make([]string, 51)}, {[]string{id}, []string{"invalid"}}} {
		_, err := uc.ReadCheckout(t.Context(), tc.p, tc.v)
		if err == nil || mustAppError(t, err).Code != apperror.CodeValidation {
			t.Fatalf("invalid batch accepted: %v", err)
		}
	}
}
