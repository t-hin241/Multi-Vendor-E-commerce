package usecase_test

import "testing"

func TestDisabledVariantPricingKeepsSKUPricesAlignedWithProductEdits(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	seedSizeAxisTemplate(f, "cat-1")
	p, err := f.products.Create(t.Context(), "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := f.products.CreateVariant(t.Context(), "user-1", p.ID, "SKU-S", []string{"opt-s"})
	if err != nil {
		t.Fatal(err)
	}
	if v.PriceAmount != 100 || v.PriceVersion != 1 {
		t.Fatalf("initial compatibility price: %+v", v)
	}
	updated, err := f.products.UpdateContent(t.Context(), "user-1", p.ID, "Sneakers", "desc", 175, p.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err = f.variants.FindByID(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PriceAmount != 175 || v.PriceAmount != 175 || v.PriceVersion != 2 {
		t.Fatalf("disabled gate stranded SKU after product edit: product=%+v variant=%+v", updated, v)
	}
}
