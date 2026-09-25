package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

type checkoutFixture struct {
	uc              *usecase.OrderUseCase
	orders          *fakeOrderRepository
	vendorOrders    *fakeVendorOrderRepository
	buyerAddresses  *fakeBuyerAddressRepository
	commissionRules *fakeCommissionRuleRepository
	cart            *fakeCartGateway
	catalog         *fakeCatalogGateway
	vendors         *fakeVendorGateway
	inventory       *fakeInventoryGateway
	shipments       *fakeShipmentGateway
	notifications   *fakeNotificationGateway
	addressID       string
}

func newCheckoutFixture() *checkoutFixture {
	vendorOrders := newFakeVendorOrderRepository()
	orders := newFakeOrderRepository(vendorOrders)
	buyerAddresses := newFakeBuyerAddressRepository()
	commissionRules := newFakeCommissionRuleRepository(1000) // 10%, matching the migration's default
	cart := newFakeCartGateway()
	catalog := newFakeCatalogGateway()
	vendors := newFakeVendorGateway()
	inventory := newFakeInventoryGateway()
	shipments := newFakeShipmentGateway(0) // fee 0 by default; tests that care override f.shipments.feeAmount
	notifications := newFakeNotificationGateway()

	uc := usecase.NewOrderUseCase(orders, vendorOrders, buyerAddresses, commissionRules, cart, catalog, vendors, inventory, shipments, notifications, zerolog.Nop())

	// Every "happy path" checkout test needs a saved address for buyer-1 —
	// seeded once here so individual tests don't repeat the boilerplate.
	address := &domain.BuyerAddress{BuyerID: "buyer-1", RecipientName: "Nguyen A", Phone: "0900000000", Province: "HN", District: "D1", Ward: "W1", StreetAddress: "123 St"}
	_ = buyerAddresses.Create(context.Background(), address)

	return &checkoutFixture{
		uc: uc, orders: orders, vendorOrders: vendorOrders, buyerAddresses: buyerAddresses, commissionRules: commissionRules,
		cart: cart, catalog: catalog, vendors: vendors, inventory: inventory, shipments: shipments, notifications: notifications,
		addressID: address.ID,
	}
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

const testToken = "buyer-1-token"

func TestCheckout_RejectsEmptyCart(t *testing.T) {
	f := newCheckoutFixture()

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestCheckout_RejectsMissingAddress(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "v1", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a missing address, got %v", appErr.Code)
	}
}

func TestCheckout_RejectsAnotherBuyersAddress(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "v1", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	other := &domain.BuyerAddress{BuyerID: "buyer-2", RecipientName: "X", Phone: "1", Province: "P", District: "D", Ward: "W", StreetAddress: "S"}
	_ = f.buyerAddresses.Create(t.Context(), other)

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, other.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for another buyer's address, got %v", appErr.Code)
	}
}

func TestCheckout_RejectsUnavailableProduct(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "v1", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: false}

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an unavailable product, got %v", appErr.Code)
	}
}

func TestCheckout_RejectsMissingVariantSelection(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "v1", Name: "Shirt", PriceAmount: 100000, Currency: "VND", IsVisible: true, HasVariants: true}

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error when a variant-having product's cart line has no variant, got %v", appErr.Code)
	}
}

func TestCheckout_ReservesByVariant(t *testing.T) {
	f := newCheckoutFixture()
	variantID := "variant-1"
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", VariantID: &variantID, Quantity: 2}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shirt", PriceAmount: 100000, Currency: "VND", IsVisible: true, HasVariants: true}
	f.catalog.variants["variant-1"] = &adapter.VariantInfo{
		ID: "variant-1", ProductID: "p1", SKU: "SHIRT-L",
		Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "L"}},
	}

	order, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reserved := f.inventory.reservedOrders[order.ID]
	if len(reserved) != 1 || reserved[0].VariantID == nil || *reserved[0].VariantID != "variant-1" {
		t.Fatalf("expected the reservation to carry the variant id, got %+v", reserved)
	}

	items, err := f.orders.ListItemsByOrder(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].VariantSKU == nil || *items[0].VariantSKU != "SHIRT-L" {
		t.Fatalf("expected the order item to snapshot the variant SKU, got %+v", items)
	}
	if items[0].VariantLabel == nil || *items[0].VariantLabel != "Size: L" {
		t.Errorf("expected the order item to snapshot a joined variant label, got %v", items[0].VariantLabel)
	}
}

func TestCheckout_RejectsMismatchedVariant(t *testing.T) {
	f := newCheckoutFixture()
	variantID := "variant-1"
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", VariantID: &variantID, Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shirt", PriceAmount: 100000, Currency: "VND", IsVisible: true, HasVariants: true}
	f.catalog.variants["variant-1"] = &adapter.VariantInfo{ID: "variant-1", ProductID: "some-other-product"}

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a variant that doesn't belong to the product, got %v", appErr.Code)
	}
}

func TestListVendorMine_IncludesItems(t *testing.T) {
	f := newCheckoutFixture()
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 2}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 100000, Currency: "VND", IsVisible: true}

	if _, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vendorOrders, items, err := f.uc.ListVendorMine(t.Context(), "user-a", "vendor-a", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vendorOrders) != 1 {
		t.Fatalf("expected one vendor order, got %d", len(vendorOrders))
	}
	voItems := items[vendorOrders[0].ID]
	if len(voItems) != 1 || voItems[0].ProductName != "Shoe" {
		t.Fatalf("expected the vendor order's own item to be included, got %+v", voItems)
	}
}

// TestListVendorMine_RejectsAVendorIDTheCallerDoesNotOwn guards the 1:N
// vendor<->user relationship: an approved vendor user still can't list
// another shop's orders just by naming that shop's vendor id.
func TestListVendorMine_RejectsAVendorIDTheCallerDoesNotOwn(t *testing.T) {
	f := newCheckoutFixture()
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-b"] = "vendor-b"

	_, _, err := f.uc.ListVendorMine(t.Context(), "user-a", "vendor-b", 20, 0)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden when naming a vendor id the caller does not own, got %v", appErr.Code)
	}
}

func TestCheckout_HappyPath_ReservesStockAndClearsCart(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{
		{ProductID: "p1", Quantity: 2},
		{ProductID: "p2", Quantity: 1},
	}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 100000, Currency: "VND", IsVisible: true}
	f.catalog.products["p2"] = &adapter.ProductInfo{ID: "p2", VendorID: "vendor-b", Name: "Hat", PriceAmount: 50000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.StatusPendingPayment {
		t.Errorf("expected pending_payment, got %q", order.Status)
	}
	wantTotal := int64(100000*2 + 50000) // shipments.feeAmount is 0 in this fixture
	if order.TotalAmount != wantTotal {
		t.Errorf("expected total %d, got %d", wantTotal, order.TotalAmount)
	}
	if order.RecipientName != "Nguyen A" || order.Province != "HN" {
		t.Errorf("expected the order to snapshot the chosen address, got %+v", order)
	}

	if _, ok := f.inventory.reservedOrders[order.ID]; !ok {
		t.Error("expected stock to be reserved for the created order")
	}
	if !f.cart.cleared[testToken] {
		t.Error("expected the cart to be cleared after a successful checkout")
	}
}

func TestCheckout_AppliesShippingFeeToTotal(t *testing.T) {
	f := newCheckoutFixture()
	f.shipments.feeAmount = 20000
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 100000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.TotalAmount != 120000 {
		t.Errorf("expected total to include the shipping fee (100000+20000), got %d", order.TotalAmount)
	}

	var found *domain.VendorOrder
	for _, vo := range f.vendorOrders.byID {
		found = vo
	}
	if found == nil || found.ShippingFeeAmount != 20000 {
		t.Fatalf("expected the vendor order to snapshot the shipping fee, got %+v", found)
	}

	created, ok := f.shipments.created[found.ID]
	if !ok {
		t.Fatal("expected a shipment to have been created for the vendor order")
	}
	if created.RecipientName != "Nguyen A" || created.Province != "HN" {
		t.Errorf("expected the shipment create call to carry the buyer's address, got %+v", created)
	}
}

// TestCheckout_SucceedsWhenShipmentCreationFails is the resilience
// guarantee behind making shipment creation best-effort at checkout: a
// briefly unreachable Shipment must never fail or roll back an otherwise-
// valid checkout, since the buyer's purchase and the stock reservation are
// already the source of truth by that point.
func TestCheckout_SucceedsWhenShipmentCreationFails(t *testing.T) {
	f := newCheckoutFixture()
	f.shipments.failAll = true
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 100000, Currency: "VND", IsVisible: true}

	order, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("expected checkout to succeed despite Shipment being unreachable, got: %v", err)
	}
	if order.TotalAmount != 100000 {
		t.Errorf("expected total to stay at just the subtotal when shipment creation failed, got %d", order.TotalAmount)
	}

	var found *domain.VendorOrder
	for _, vo := range f.vendorOrders.byID {
		found = vo
	}
	if found == nil || found.ShippingFeeAmount != 0 {
		t.Errorf("expected no shipping fee to be snapshotted when shipment creation failed, got %+v", found)
	}
}

func TestCheckout_InsufficientStock_CancelsOrderAndDoesNotClearCart(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 10}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 100000, Currency: "VND", IsVisible: true}
	f.inventory.shortProduct = "p1"

	_, err := f.uc.Checkout(t.Context(), "buyer-1", testToken, f.addressID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for insufficient stock, got %v", appErr.Code)
	}

	// The order that was created as part of the saga must end up cancelled,
	// not left dangling in pending_payment.
	var found *domain.Order
	for _, o := range f.orders.byID {
		found = o
	}
	if found == nil {
		t.Fatal("expected an order to have been created before the reservation failed")
	}
	if found.Status != domain.StatusCancelled {
		t.Errorf("expected the order to be cancelled after a failed reservation, got %q", found.Status)
	}

	if f.cart.cleared[testToken] {
		t.Error("the cart must not be cleared when checkout fails")
	}
}

func TestCancel_OnlyPendingPaymentCanBeCancelled(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	ctx := t.Context()

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cancelled, err := f.uc.Cancel(ctx, "buyer-1", order.ID)
	if err != nil {
		t.Fatalf("unexpected error cancelling: %v", err)
	}
	if cancelled.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", cancelled.Status)
	}
	if !f.inventory.releasedOrders[order.ID] {
		t.Error("expected inventory to be released for a cancelled order")
	}
	var vendorOrderID string
	for _, vo := range f.vendorOrders.byID {
		vendorOrderID = vo.ID
	}
	if !f.shipments.cancelled[vendorOrderID] {
		t.Error("expected the vendor order's shipment to be cancelled too")
	}

	// Cancelling an already-cancelled order must fail.
	_, err = f.uc.Cancel(ctx, "buyer-1", order.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict re-cancelling, got %v", appErr.Code)
	}
}

func TestCancel_RejectsNonOwner(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	ctx := t.Context()

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.Cancel(ctx, "buyer-2", order.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner cancel, got %v", appErr.Code)
	}
}

func TestGetOwnedByBuyer_RejectsNonOwner(t *testing.T) {
	f := newCheckoutFixture()
	f.cart.byToken[testToken] = []adapter.CartLine{{ProductID: "p1", Quantity: 1}}
	f.catalog.products["p1"] = &adapter.ProductInfo{ID: "p1", VendorID: "vendor-a", Name: "Shoe", PriceAmount: 1000, Currency: "VND", IsVisible: true}
	ctx := t.Context()

	order, err := f.uc.Checkout(ctx, "buyer-1", testToken, f.addressID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, _, err = f.uc.GetOwnedByBuyer(ctx, "someone-else", order.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}
