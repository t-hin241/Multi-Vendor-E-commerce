package usecase_test

import (
	"context"
	"testing"

	"shopee/backend/pkg/purchasequote"
	"shopee/backend/services/cart/internal/domain"
)

func TestVacationKeepsCartLineButBlocksNewPurchases(t *testing.T) {
	f := newFixture()
	p := f.sellable("vacation-product", 1000)
	if err := f.uc.AddItem(t.Context(), "buyer", p.ID, nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	before := f.view(t, "buyer")
	paused := true
	f.catalog.quoteFunc = func(_ context.Context, inputs []purchasequote.Input) (*purchasequote.Quote, error) {
		out := &purchasequote.Quote{QuoteVersion: "fake-vacation-quote"}
		for _, in := range inputs {
			out.Lines = append(out.Lines, purchasequote.Line{Input: in, ProductFound: true, ProductName: p.Name, Status: "approved", UnitPrice: p.PriceAmount, Currency: p.Currency, IsSellable: !paused, ShopPaused: paused})
		}
		return out, nil
	}
	v := f.view(t, "buyer")
	if len(v.Lines) != 1 || v.Lines[0].State != domain.LineShopPaused || v.Lines[0].Quantity != 2 || v.CheckoutReady || v.UnavailableLines != 1 {
		t.Fatalf("paused cart: %+v", v)
	}
	expectCode(t, f.uc.AddItem(t.Context(), "buyer", p.ID, nil, 1, nil), "vendor_not_accepting_orders")
	expectCode(t, f.uc.AddItem(t.Context(), "another-buyer", p.ID, nil, 1, nil), "vendor_not_accepting_orders")
	paused = false
	v = f.view(t, "buyer")
	if len(v.Lines) != 1 || v.Lines[0].State != domain.LineAvailable || v.Lines[0].Quantity != 2 || !v.CheckoutReady || v.Version != before.Version {
		t.Fatalf("resume changed cart: %+v", v)
	}
}
