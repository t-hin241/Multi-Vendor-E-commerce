package usecase_test

import (
	"shopee/backend/pkg/apperror"
	"testing"
)

func TestPublicProductDisappearsWhenVendorSuspended(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Test product", "Description", 100000, nil)
	if err != nil {
		t.Fatal(err)
	}
	submitForReview(t, f, "user-1", p.ID)
	if _, err = f.products.Approve(ctx, p.ID, "admin-1"); err != nil {
		t.Fatal(err)
	}
	f.vendors.saleErr = apperror.Conflict("Shop suspended")
	if _, _, _, _, _, _, _, _, err = f.products.GetPublicBySlug(ctx, p.Slug, "", ""); err == nil {
		t.Fatal("suspended shop product remains visible")
	}
	lookup, _, _, err := f.products.GetByIDForOwnerLookup(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.IsPubliclyVisible() {
		t.Fatal("cart lookup advertises suspended shop as sellable")
	}
}
