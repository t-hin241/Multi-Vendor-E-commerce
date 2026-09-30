package usecase_test

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/usecase"
)

type fixture struct {
	uc        *usecase.CartUseCase
	store     *fakeStore
	catalog   *fakeCatalogGateway
	inventory *fakeInventoryGateway
}

func newFixture() *fixture {
	store := newFakeStore()
	catalog := newFakeCatalogGateway()
	inventory := newFakeInventoryGateway()
	uc := usecase.NewCartUseCase(fakeTx{store}, fakeCartRepository{store}, fakeCartItemRepository{store},
		fakeOperationRepository{store}, catalog, inventory, zerolog.Nop())
	return &fixture{uc: uc, store: store, catalog: catalog, inventory: inventory}
}

// sellable registers a visible product priced in VND with plenty of stock.
func (f *fixture) sellable(id string, price int64) *adapter.ProductInfo {
	p := &adapter.ProductInfo{ID: id, Name: "Product " + id, PriceAmount: price, Currency: "VND", Status: "approved", IsVisible: true}
	f.catalog.products[id] = p
	f.inventory.productStock[id] = 100
	return p
}

func (f *fixture) view(t *testing.T, userID string) *usecase.CartView {
	t.Helper()
	v, err := f.uc.View(t.Context(), userID, usecase.Page{Limit: usecase.DefaultPageLimit})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return v
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func expectCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	if got := mustAppError(t, err).Code; got != code {
		t.Fatalf("expected %s, got %s (%v)", code, got, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAddItem_RejectsUnsellableProduct(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100).IsVisible = false

	expectCode(t, f.uc.AddItem(t.Context(), "user-1", "product-1", nil, 2, nil), apperror.CodeValidation)
}

func TestAddItem_RejectsQuantityOutOfRange(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)

	for _, q := range []int64{0, -1, domain.MaxQuantityPerLine + 1} {
		expectCode(t, f.uc.AddItem(t.Context(), "user-1", "product-1", nil, q, nil), apperror.CodeValidation)
	}
}

func TestAddItem_AccumulatesQuantityUpToTheLimit(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100000)
	ctx := t.Context()

	if err := f.uc.AddItem(ctx, "user-1", "product-1", nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.AddItem(ctx, "user-1", "product-1", nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	v := f.view(t, "user-1")
	if len(v.Lines) != 1 || v.Lines[0].Quantity != 5 {
		t.Fatalf("expected a single line with quantity 5, got %+v", v.Lines)
	}

	expectCode(t, f.uc.AddItem(ctx, "user-1", "product-1", nil, domain.MaxQuantityPerLine-4, nil), apperror.CodeValidation)
	if got := f.view(t, "user-1").Lines[0].Quantity; got != 5 {
		t.Fatalf("a rejected add must not change the line, got %d", got)
	}
}

func TestAddItem_RejectsLineOverCartLimit(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	for i := range domain.MaxLinesPerCart {
		id := "product-" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		f.sellable(id, 100)
		if err := f.uc.AddItem(ctx, "user-1", id, nil, 1, nil); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	f.sellable("one-too-many", 100)
	expectCode(t, f.uc.AddItem(ctx, "user-1", "one-too-many", nil, 1, nil), apperror.CodeValidation)
}

func TestAddItem_RejectsMixedCurrency(t *testing.T) {
	f := newFixture()
	f.sellable("product-vnd", 100)
	f.sellable("product-usd", 5).Currency = "USD"
	ctx := t.Context()

	if err := f.uc.AddItem(ctx, "user-1", "product-vnd", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	expectCode(t, f.uc.AddItem(ctx, "user-1", "product-usd", nil, 1, nil), apperror.CodeValidation)
}

func TestAddItem_RequiresVariantWhenProductHasVariants(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100).HasVariants = true

	expectCode(t, f.uc.AddItem(t.Context(), "user-1", "product-1", nil, 1, nil), apperror.CodeValidation)
}

func TestAddItem_RejectsMismatchedVariant(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100).HasVariants = true
	f.catalog.variants["variant-1"] = &adapter.VariantInfo{ID: "variant-1", ProductID: "some-other-product"}

	expectCode(t, f.uc.AddItem(t.Context(), "user-1", "product-1", ptr("variant-1"), 1, nil), apperror.CodeValidation)
}

func TestAddItem_KeepsDifferentVariantsAsSeparateLines(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100000).HasVariants = true
	f.catalog.variants["variant-s"] = &adapter.VariantInfo{ID: "variant-s", ProductID: "product-1", SKU: "SHIRT-S", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "S"}}}
	f.catalog.variants["variant-m"] = &adapter.VariantInfo{ID: "variant-m", ProductID: "product-1", SKU: "SHIRT-M", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "M"}}}
	f.inventory.variantStock["variant-s"], f.inventory.variantStock["variant-m"] = 10, 10
	ctx := t.Context()

	for _, step := range []struct {
		variant string
		qty     int64
	}{{"variant-s", 2}, {"variant-m", 1}, {"variant-s", 3}} {
		if err := f.uc.AddItem(ctx, "user-1", "product-1", ptr(step.variant), step.qty, nil); err != nil {
			t.Fatal(err)
		}
	}

	v := f.view(t, "user-1")
	if len(v.Lines) != 2 {
		t.Fatalf("expected two lines, got %+v", v.Lines)
	}
	for _, l := range v.Lines {
		want := map[string]int64{"variant-s": 5, "variant-m": 1}[*l.VariantID]
		if l.Quantity != want || l.VariantLabel == nil || *l.VariantLabel == "" || l.State != domain.LineAvailable {
			t.Errorf("unexpected line %+v", l)
		}
	}
}

func TestMutations_ExpectedVersionMismatchIsCartChanged(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)
	ctx := t.Context()

	if err := f.uc.AddItem(ctx, "user-1", "product-1", nil, 1, ptr(int64(1))); err != nil {
		t.Fatal(err)
	}
	v := f.view(t, "user-1")
	if v.Version != 2 {
		t.Fatalf("expected version 2 after one change, got %d", v.Version)
	}

	expectCode(t, f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, 3, ptr(int64(1))), domain.CodeCartChanged)
	expectCode(t, f.uc.RemoveItem(ctx, "user-1", "product-1", nil, ptr(int64(1))), domain.CodeCartChanged)
	expectCode(t, f.uc.Clear(ctx, "user-1", ptr(int64(1))), domain.CodeCartChanged)

	if err := f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, 3, ptr(v.Version)); err != nil {
		t.Fatalf("matching version must succeed: %v", err)
	}
	if got := f.view(t, "user-1"); got.Version != 3 || got.Lines[0].Quantity != 3 {
		t.Fatalf("unexpected cart %+v", got)
	}
}

func TestSetItemQuantity_ZeroRemovesAndNegativeIsRejected(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)
	ctx := t.Context()

	if err := f.uc.AddItem(ctx, "user-1", "product-1", nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	expectCode(t, f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, -1, nil), apperror.CodeValidation)
	expectCode(t, f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, domain.MaxQuantityPerLine+1, nil), apperror.CodeValidation)
	if err := f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, 0, nil); err != nil {
		t.Fatal(err)
	}
	if v := f.view(t, "user-1"); len(v.Lines) != 0 {
		t.Errorf("expected the cart to be empty, got %+v", v.Lines)
	}
}

func TestRemoveItem_IsIdempotentAndKeepsVersion(t *testing.T) {
	f := newFixture()
	if err := f.uc.RemoveItem(t.Context(), "user-1", "never-added", nil, nil); err != nil {
		t.Fatalf("removing a never-added item must succeed, got: %v", err)
	}
	if v := f.view(t, "user-1"); v.Version != 1 {
		t.Errorf("a no-op removal must not bump the version, got %d", v.Version)
	}
}

func TestBuyersOnlySeeAndChangeTheirOwnCart(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)
	ctx := t.Context()

	if err := f.uc.AddItem(ctx, "buyer-a", "product-1", nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.Clear(ctx, "buyer-b", nil); err != nil {
		t.Fatal(err)
	}
	if v := f.view(t, "buyer-b"); len(v.Lines) != 0 {
		t.Fatalf("buyer B must not see buyer A's lines: %+v", v.Lines)
	}
	if v := f.view(t, "buyer-a"); len(v.Lines) != 1 || v.Lines[0].Quantity != 2 {
		t.Fatalf("buyer B's clear must not touch buyer A's cart: %+v", v.Lines)
	}
}

func TestView_ReportsLineStatesWithoutDroppingLines(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	for _, id := range []string{"ok", "gone", "paused", "review", "empty", "short", "timeout"} {
		f.sellable(id, 1000)
		if err := f.uc.AddItem(ctx, "user-1", id, nil, 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	delete(f.catalog.products, "gone")
	f.catalog.products["paused"].IsVisible = false
	f.catalog.products["review"].Status, f.catalog.products["review"].IsVisible = "pending_review", false
	f.inventory.productStock["empty"] = 0
	f.inventory.productStock["short"] = 1
	f.catalog.failing["timeout"] = true

	v := f.view(t, "user-1")
	want := map[string]domain.LineState{
		"ok": domain.LineAvailable, "gone": domain.LineRemoved, "paused": domain.LineNotForSale, "review": domain.LineUnderReview,
		"empty": domain.LineOutOfStock, "short": domain.LineInsufficientStock, "timeout": domain.LineUnverified,
	}
	if len(v.Lines) != len(want) {
		t.Fatalf("every line must stay visible, got %d", len(v.Lines))
	}
	for _, l := range v.Lines {
		if l.State != want[l.ProductID] {
			t.Errorf("%s: expected %s, got %s", l.ProductID, want[l.ProductID], l.State)
		}
	}
	if v.Subtotal == nil || *v.Subtotal != 2000 || v.Currency != "VND" {
		t.Errorf("subtotal must only count purchasable lines, got %v %s", v.Subtotal, v.Currency)
	}
	if v.CheckoutReady || !v.CatalogDegraded || v.UnavailableLines != 6 {
		t.Errorf("unexpected cart flags %+v", v)
	}
	for _, l := range v.Lines {
		if l.ProductID == "timeout" && (l.PriceAmount != nil || l.Subtotal != nil) {
			t.Errorf("an unverified line must not be priced from a stale value: %+v", l)
		}
		if l.ProductID == "short" && (l.AvailableQuantity == nil || *l.AvailableQuantity != 1) {
			t.Errorf("expected the remaining stock to be reported, got %+v", l.AvailableQuantity)
		}
	}
}

func TestView_InventoryOutageDoesNotBlockCheckout(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)
	if err := f.uc.AddItem(t.Context(), "user-1", "product-1", nil, 1, nil); err != nil {
		t.Fatal(err)
	}
	f.inventory.down = true

	v := f.view(t, "user-1")
	if !v.InventoryDegraded || v.Lines[0].Stock != domain.StockUnknown || !v.CheckoutReady {
		t.Fatalf("stock is display-only; Inventory's reservation decides at checkout: %+v", v)
	}
}

func TestView_MixedLegacyCurrenciesHaveNoSubtotal(t *testing.T) {
	f := newFixture()
	f.sellable("a", 100)
	f.sellable("b", 100)
	ctx := t.Context()
	for _, id := range []string{"a", "b"} {
		if err := f.uc.AddItem(ctx, "user-1", id, nil, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The vendor re-priced b in another currency after it was added. Its
	// price also changed, so confirm it to isolate the currency rule.
	f.catalog.products["b"].Currency = "USD"
	v := f.view(t, "user-1")
	if !v.MixedCurrency || v.Subtotal != nil || v.CheckoutReady {
		t.Fatalf("mixed currencies must not be summed: %+v", v)
	}
}

func TestView_PaginatesLinesButTotalsCoverTheCart(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	for _, id := range []string{"a", "b", "c"} {
		f.sellable(id, 10)
		if err := f.uc.AddItem(ctx, "user-1", id, nil, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	v, err := f.uc.View(ctx, "user-1", usecase.Page{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Lines) != 1 || v.Lines[0].ProductID != "c" || v.TotalLines != 3 || *v.Subtotal != 30 {
		t.Fatalf("unexpected page %+v", v)
	}
}

func TestView_DeduplicatesCatalogLookups(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100).HasVariants = true
	ctx := t.Context()
	for _, id := range []string{"v1", "v2", "v3"} {
		f.catalog.variants[id] = &adapter.VariantInfo{ID: id, ProductID: "product-1"}
		f.inventory.variantStock[id] = 5
		if err := f.uc.AddItem(ctx, "user-1", "product-1", ptr(id), 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	f.catalog.calls = 0
	f.view(t, "user-1")
	if f.catalog.calls != 4 {
		t.Fatalf("expected 1 product + 3 variant lookups, got %d", f.catalog.calls)
	}
}

func TestPriceChange_IsFlaggedUntilTheBuyerConfirmsTheLivePrice(t *testing.T) {
	f := newFixture()
	f.sellable("product-1", 100)
	ctx := t.Context()
	if err := f.uc.AddItem(ctx, "user-1", "product-1", nil, 2, nil); err != nil {
		t.Fatal(err)
	}
	f.catalog.products["product-1"].PriceAmount = 120

	v := f.view(t, "user-1")
	line := v.Lines[0]
	if !line.PriceChanged || *line.SeenPriceAmount != 100 || *line.PriceAmount != 120 || v.CheckoutReady {
		t.Fatalf("expected a flagged price change, got %+v", line)
	}

	// Changing the quantity is not accepting the new price.
	if err := f.uc.SetItemQuantity(ctx, "user-1", "product-1", nil, 3, nil); err != nil {
		t.Fatal(err)
	}
	v = f.view(t, "user-1")
	if !v.Lines[0].PriceChanged {
		t.Fatal("a quantity change must not clear the price-changed flag")
	}

	// Confirming a price that is no longer live is rejected.
	stale := []usecase.PriceConfirmation{{LineID: line.LineID, PriceAmount: 110, Currency: "VND"}}
	expectCode(t, f.uc.ConfirmPrices(ctx, "user-1", v.Version, stale), domain.CodeCartChanged)
	// Confirming against an old cart version is rejected.
	live := []usecase.PriceConfirmation{{LineID: line.LineID, PriceAmount: 120, Currency: "VND"}}
	expectCode(t, f.uc.ConfirmPrices(ctx, "user-1", v.Version-1, live), domain.CodeCartChanged)

	if err := f.uc.ConfirmPrices(ctx, "user-1", v.Version, live); err != nil {
		t.Fatal(err)
	}
	if v := f.view(t, "user-1"); v.Lines[0].PriceChanged || !v.CheckoutReady {
		t.Fatalf("expected the confirmed cart to be ready, got %+v", v)
	}
}
