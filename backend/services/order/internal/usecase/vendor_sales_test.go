package usecase_test

import (
	"errors"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"testing"
)

func TestCheckoutRejectsRevokedOrUnavailableLiveShopBeforeOrderCreation(t *testing.T) {
	for _, err := range []error{apperror.Conflict("Shop suspended"), apperror.Internal(errors.New("test vendor outage"))} {
		f := newCheckoutFixture()
		f.cart.byBuyer["buyer-1"] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
		f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "v1", Name: "Test product", PriceAmount: 1000, Currency: "VND", IsVisible: true}
		f.vendors.saleErr = err
		if _, err := f.uc.Checkout(t.Context(), "buyer-1", f.addressID, nil); err == nil {
			t.Fatal("checkout accepted without live selling permission")
		}
		if len(f.orders.byID) != 0 || len(f.inventory.reservedOrders) != 0 {
			t.Fatal("order or reservation created before selling permission")
		}
	}
}
