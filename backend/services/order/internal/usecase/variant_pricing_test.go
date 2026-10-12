package usecase_test

import (
	"context"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
	"testing"
)

func skuCheckoutFixture() (*checkoutFixture, *adapter.VariantInfo) {
	f := newCheckoutFixture()
	f.catalog.pricingEnabled = true
	f.product("p", "shop", 100).HasVariants = true
	v := &adapter.VariantInfo{ID: "sku", ProductID: "p", SKU: "SKU-A", PriceAmount: 150, PriceVersion: 1}
	f.catalog.variants[v.ID] = v
	price, version, currency := int64(150), int64(1), "VND"
	f.cartOf(adapter.CartLine{ProductID: "p", VariantID: &v.ID, Quantity: 2, SeenPriceAmount: &price, SeenCurrency: &currency, SeenPricingVersion: &version})
	return f, v
}

func TestSKUCheckoutRequiresCurrentQuoteBeforeReserving(t *testing.T) {
	for _, mode := range []string{"missing_preview", "stale_preview", "changed_during_shipping", "fake_seen_price"} {
		t.Run(mode, func(t *testing.T) {
			f, v := skuCheckoutFixture()
			preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
			if err != nil {
				t.Fatal(err)
			}
			input := usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "sku-" + mode, ExpectedQuoteVersion: preview.QuoteVersion}
			switch mode {
			case "missing_preview":
				input.ExpectedQuoteVersion = ""
			case "stale_preview":
				f.catalog.products["p"].Version++
			case "fake_seen_price":
				price, version, currency := int64(1), int64(1), "VND"
				f.cartOf(adapter.CartLine{ProductID: "p", VariantID: &v.ID, Quantity: 2, SeenPriceAmount: &price, SeenCurrency: &currency, SeenPricingVersion: &version})
			case "changed_during_shipping":
				f.uc.Shipments = quoteGateway{f.shipments, func(ctx context.Context, id, province string, weight int64) (*domain.ShippingQuote, error) {
					v.PriceVersion++
					return f.shipments.Quote(ctx, id, province, weight)
				}}
			}
			_, _, err = f.uc.Checkout(t.Context(), "buyer-1", input)
			expectCode(t, err, "price_changed")
			if len(f.orders.byID) > 0 || len(f.inventory.reservedOrders) > 0 {
				t.Fatal("stale quote created an order/reservation")
			}
		})
	}
}

func TestSKUOrderAndReturnKeepPurchasedPriceAfterCatalogChange(t *testing.T) {
	f, v := skuCheckoutFixture()
	preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
	if err != nil {
		t.Fatal(err)
	}
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "sku-history", ExpectedQuoteVersion: preview.QuoteVersion, ExpectedTotal: preview.TotalAmount})
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.orders.ListItemsByOrder(t.Context(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PriceAmount != 150 || items[0].PricingSource != "variant" || items[0].CatalogVariantVersion != 1 || order.SubtotalAmount != 300 {
		t.Fatalf("wrong SKU snapshots: %+v %+v", order, items)
	}
	v.PriceAmount = 999
	v.PriceVersion = 2
	f.pay(t, order, "99999999-9999-9999-9999-999999999999")
	f.vendors.approvedVendors["seller"] = "shop"
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	if _, err = f.uc.UpdateVendorOrderStatus(t.Context(), "seller", vos[0].ID, domain.StatusProcessing); err != nil {
		t.Fatal(err)
	}
	shipmentEvent(t, f, vos[0].ID, "delivered")
	rr, err := f.uc.CreateReturn(t.Context(), "buyer-1", usecase.ReturnInput{OrderID: order.ID, ItemID: items[0].ID, Quantity: 1, Reason: "Broken", Evidence: "synthetic photo"})
	if err != nil {
		t.Fatal(err)
	}
	if rr.RefundAmount != 150 || f.orders.get(order.ID).SubtotalAmount != 300 {
		t.Fatalf("history repriced: %+v", rr)
	}
}
