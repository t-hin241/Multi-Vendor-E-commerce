package usecase_test

import (
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/usecase"
	"testing"
)

func TestSKUPriceChangeRequiresVersionConfirmationEvenAfterAddingQuantity(t *testing.T) {
	f := newFixture()
	f.sellable("p", 100).HasVariants = true
	v := &adapter.VariantInfo{ID: "v", ProductID: "p", SKU: "A", PriceAmount: 150, PriceVersion: 1}
	f.catalog.variants[v.ID] = v
	f.inventory.variantStock[v.ID] = 20
	if err := f.uc.AddItem(t.Context(), "buyer", "p", &v.ID, 2, nil); err != nil {
		t.Fatal(err)
	}
	view := f.view(t, "buyer")
	if *view.Lines[0].PriceAmount != 150 || view.Lines[0].PriceChanged {
		t.Fatal("did not remember SKU price")
	}
	v.PriceAmount = 180
	v.PriceVersion = 2
	if err := f.uc.AddItem(t.Context(), "buyer", "p", &v.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	view = f.view(t, "buyer")
	if !view.Lines[0].PriceChanged || *view.Lines[0].SeenPriceAmount != 150 {
		t.Fatal("quantity accepted price implicitly")
	}
	c := usecase.PriceConfirmation{LineID: view.Lines[0].LineID, VariantID: &v.ID, PriceAmount: 180, Currency: "VND", PricingVersion: 1}
	expectCode(t, f.uc.ConfirmPrices(t.Context(), "buyer", view.Version, []usecase.PriceConfirmation{c}), "price_changed")
	c.PricingVersion = 2
	if err := f.uc.ConfirmPrices(t.Context(), "buyer", view.Version, []usecase.PriceConfirmation{c}); err != nil {
		t.Fatal(err)
	}
	if view = f.view(t, "buyer"); !view.CheckoutReady {
		t.Fatal("confirmed SKU not ready")
	}
	// Same amount after another revision still needs its current version.
	v.PriceVersion = 3
	if !f.view(t, "buyer").Lines[0].PriceChanged {
		t.Fatal("version ignored")
	}
}
func TestPurchaseQuoteOutageNeverUsesRememberedPrices(t *testing.T) {
	f := newFixture()
	f.sellable("p", 100)
	if err := f.uc.AddItem(t.Context(), "buyer", "p", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	f.catalog.failing["p"] = true
	view := f.view(t, "buyer")
	if !view.CatalogDegraded || view.CheckoutReady || view.Lines[0].State != domain.LineUnverified || view.Lines[0].PriceAmount != nil {
		t.Fatal("outage used stale price")
	}
}
