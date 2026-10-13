package usecase_test

import (
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/usecase"
)

const selectedA = "00000000-0000-0000-0000-000000000001"
const selectedB = "00000000-0000-0000-0000-000000000002"

func selectiveFixture() *checkoutFixture {
	f := newCheckoutFixture()
	f.uc.SelectiveCheckoutEnabled = true
	f.product("p1", "v1", 1000)
	f.product("p2", "v2", 2000)
	f.cartOf(adapter.CartLine{LineID: selectedA, ProductID: "p1", Quantity: 2}, adapter.CartLine{LineID: selectedB, ProductID: "p2", Quantity: 1})
	return f
}

func TestSelectiveCheckoutIgnoresUnavailableUnselectedShopAndReplaysAfterRollback(t *testing.T) {
	f := selectiveFixture()
	delete(f.catalog.products, "p2")
	version := int64(1)
	ids := []string{selectedA}
	quote, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID, usecase.SelectionInput{CartVersion: &version, SelectedLineIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(quote.Vendors) != 1 || quote.SubtotalAmount != 2000 || *quote.TotalAmount != 22000 {
		t.Fatalf("wrong selected quote: %+v", quote)
	}
	in := usecase.CheckoutInput{AddressID: f.addressID, CartVersion: &version, SelectedLineIDs: ids, ExpectedTotal: quote.TotalAmount, IdempotencyKey: "selected-order-1"}
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", in)
	if err != nil {
		t.Fatal(err)
	}
	consumed := f.cart.consumed["buyer-1"]
	if len(consumed) != 1 || consumed[0].LineID != selectedA || consumed[0].Quantity != 2 {
		t.Fatalf("wrong consume: %+v", consumed)
	}
	f.cart.byBuyer["buyer-1"] = nil // a retry cannot need the live cart
	f.uc.SelectiveCheckoutEnabled = false
	again, replayed, err := f.uc.Checkout(t.Context(), "buyer-1", in)
	if err != nil || !replayed || again.ID != order.ID {
		t.Fatalf("retry after consume: %+v %v", again, err)
	}
	in.SelectedLineIDs = []string{selectedB}
	if _, _, err = f.uc.Checkout(t.Context(), "buyer-1", in); err == nil {
		t.Fatal("same key accepted different selection")
	}
}

func TestSelectivePreviewRejectsUnknownStaleAndSelectedPriceChange(t *testing.T) {
	for _, scenario := range []string{"unknown", "stale", "missing-version", "disabled", "price"} {
		t.Run(scenario, func(t *testing.T) {
			f := selectiveFixture()
			version := int64(1)
			selection := usecase.SelectionInput{CartVersion: &version, SelectedLineIDs: []string{selectedA}}
			switch scenario {
			case "unknown":
				selection.SelectedLineIDs = []string{"00000000-0000-0000-0000-000000000099"}
			case "stale":
				version = 2
			case "missing-version":
				selection.CartVersion = nil
			case "disabled":
				f.uc.SelectiveCheckoutEnabled = false
			case "price":
				old, currency := int64(500), "VND"
				f.cart.byBuyer["buyer-1"][0].SeenPriceAmount = &old
				f.cart.byBuyer["buyer-1"][0].SeenCurrency = &currency
			}
			if _, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID, selection); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestTwoShopSelectionCanonicalRetryAndConsumeRecovery(t *testing.T) {
	f := selectiveFixture()
	version := int64(1)
	ids := []string{selectedB, selectedA}
	quote, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID, usecase.SelectionInput{CartVersion: &version, SelectedLineIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(quote.Vendors) != 2 || *quote.TotalAmount != 44000 {
		t.Fatalf("two shop total: %+v", quote)
	}
	f.cart.consumeErr = apperror.Internal(errors.New("fake Cart outage"))
	in := usecase.CheckoutInput{AddressID: f.addressID, CartVersion: &version, SelectedLineIDs: ids, ExpectedTotal: quote.TotalAmount, IdempotencyKey: "selection-recovery"}
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", in)
	if err != nil {
		t.Fatal(err)
	}
	in.SelectedLineIDs = []string{selectedA, selectedB}
	retry, replayed, err := f.uc.Checkout(t.Context(), "buyer-1", in)
	if err != nil || !replayed || retry.ID != order.ID {
		t.Fatalf("reordered retry: %v", err)
	}
	f.cart.consumeErr = nil
	if _, err := f.uc.ProcessCartConsumptions(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if len(f.cart.consumed["buyer-1"]) != 2 {
		t.Fatal("worker did not consume frozen selection")
	}
}
