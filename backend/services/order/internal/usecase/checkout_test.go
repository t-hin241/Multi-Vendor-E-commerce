package usecase_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

func TestCheckout_TwoVendorsSnapshotsPricesShippingAndCommission(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 100000)
	f.product("p2", "vendor-b", 50000)
	f.shipments.feeByVendor["vendor-b"] = 15000
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 2}, adapter.CartLine{ProductID: "p2", Quantity: 1})

	order := f.checkout(t, "key-00000001")

	if order.SubtotalAmount != 250000 || order.ShippingAmount != 35000 || order.TotalAmount != 285000 {
		t.Fatalf("unexpected totals %+v", order)
	}
	if order.CheckoutState != domain.CheckoutReady {
		t.Fatalf("a reserved order must be ready, got %s", order.CheckoutState)
	}
	vendorOrders, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	var sum int64
	for _, vo := range vendorOrders {
		sum += vo.Total()
		if vo.Shipping == nil || vo.Commission == nil || *vo.Commission.RuleVersion != 1 || vo.Commission.BaseAmount != vo.SubtotalAmount {
			t.Errorf("vendor order %s is missing its snapshots: %+v", vo.VendorID, vo)
		}
	}
	if sum != order.TotalAmount {
		t.Fatalf("parent total %d must equal vendor totals %d", order.TotalAmount, sum)
	}
	if _, ok := f.inventory.reservedOrders[order.ID]; !ok {
		t.Fatal("stock must be reserved")
	}
	if len(f.shipments.created) != 0 {
		t.Fatal("no shipment may be created before payment")
	}
	if task := f.consumptions.get(order.ID); task == nil || task.Status != domain.CartConsumptionConsumed {
		t.Fatalf("expected the purchased cart lines to be consumed, got %+v", task)
	}

	// A later price, fee or commission change never alters the order.
	f.catalog.products["p1"].PriceAmount = 1
	f.shipments.fee = 1
	if _, err := f.uc.SetCommissionRule(t.Context(), "admin-1", 5000); err != nil {
		t.Fatal(err)
	}
	if again := f.orders.get(order.ID); again.TotalAmount != 285000 {
		t.Fatalf("order total changed to %d", again.TotalAmount)
	}
}

func TestCheckout_SameKeyReturnsTheSameOrderAndDifferentRequestConflicts(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	first := f.checkout(t, "key-00000002")
	order, replayed, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000002"})
	if err != nil || !replayed || order.ID != first.ID {
		t.Fatalf("expected the same order, got %+v %v %v", order, replayed, err)
	}
	if len(f.orders.byID) != 1 || len(f.inventory.reservedOrders) != 1 {
		t.Fatal("a retry must not create another order or reservation")
	}
	version := int64(7)
	_, _, err = f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, CartVersion: &version, IdempotencyKey: "key-00000002"})
	expectCode(t, err, domain.CodeIdempotencyKeyReused)
}

func TestCheckout_ConcurrentDoubleClickCreatesOneOrder(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-double-click"})
			if err == nil {
				mu.Lock()
				ids[order.ID] = true
				mu.Unlock()
				return
			}
			var app *apperror.Error
			if !errors.As(err, &app) || app.Code != domain.CodeCheckoutInProgress {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if len(f.orders.byID) != 1 || len(ids) > 1 {
		t.Fatalf("expected exactly one order, got %d orders (%v)", len(f.orders.byID), ids)
	}
}

func TestCheckout_ShippingUnavailableCreatesNoOrder(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.shipments.unavailable["vendor-a"] = true
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000003"})
	expectCode(t, err, domain.CodeShippingUnavailable)
	if len(f.orders.byID) != 0 {
		t.Fatal("a missing shipping quote must not create a payable order")
	}
	// The same key replays the refusal.
	_, _, err = f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000003"})
	expectCode(t, err, domain.CodeShippingUnavailable)
}

func TestCheckout_RefusesATotalTheBuyerDidNotConfirm(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	stale := int64(1000 + 15000) // reviewed before the fee rose to 20000
	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, ExpectedTotal: &stale, IdempotencyKey: "key-00000004"})
	expectCode(t, err, domain.CodeCheckoutTotalChanged)
	if len(f.orders.byID) != 0 {
		t.Fatal("no order at a total the buyer did not see")
	}
	current := int64(21000)
	if _, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, ExpectedTotal: &current, IdempotencyKey: "key-00000005"}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckout_InfrastructureFailureCanBeRetriedWithTheSameKey(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
	f.shipments.quoteErr = apperror.Internal(errors.New("shipment down"))

	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000006"})
	expectCode(t, err, apperror.CodeInternal)
	f.shipments.quoteErr = nil
	if _, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000006"}); err != nil {
		t.Fatalf("a retry after an outage must succeed: %v", err)
	}
}

func TestCheckout_InsufficientStockCancelsDurablyAndKeepsCart(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.inventory.shortProduct = "p1"
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 10})

	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000007"})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict || appErr.Message != "Not enough stock available for product p1" {
		t.Fatalf("expected Inventory's conflict, got %v", err)
	}
	var order *domain.Order
	for _, o := range f.orders.byID {
		order = o
	}
	if order == nil || order.Status != domain.StatusCancelled || order.CheckoutState != domain.CheckoutFailed {
		t.Fatalf("the order must be cancelled and not payable, got %+v", order)
	}
	if !f.inventory.releasedOrders[order.ID] {
		t.Error("the release tombstone must be sent")
	}
	if len(f.cart.consumed["buyer-1"]) != 0 {
		t.Error("the cart must not be consumed when checkout fails")
	}
	vos, _ := f.vendorOrders.ListByOrderID(t.Context(), order.ID)
	if vos[0].Status != domain.StatusCancelled {
		t.Errorf("vendor orders are cancelled with the parent, got %s", vos[0].Status)
	}
}

func TestCheckout_ValidationErrorsCreateNothing(t *testing.T) {
	cases := map[string]func(f *checkoutFixture) usecase.CheckoutInput{
		"empty cart": func(f *checkoutFixture) usecase.CheckoutInput {
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
		"missing address": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000)
			f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
			return usecase.CheckoutInput{}
		},
		"unavailable product": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000).IsVisible = false
			f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
		"variant required": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000).HasVariants = true
			f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
		"variant of another product": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000).HasVariants = true
			f.catalog.variants["v1"] = &adapter.VariantInfo{ID: "v1", ProductID: "other"}
			v := "v1"
			f.cartOf(adapter.CartLine{ProductID: "p1", VariantID: &v, Quantity: 1})
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
		"unaccepted price": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000)
			seen, cur := int64(900), "VND"
			f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1, SeenPriceAmount: &seen, SeenCurrency: &cur})
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
		"shop not selling": func(f *checkoutFixture) usecase.CheckoutInput {
			f.product("p1", "vendor-a", 1000)
			f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
			f.vendors.saleErr = apperror.Conflict("Shop suspended")
			return usecase.CheckoutInput{AddressID: f.addressID}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCheckoutFixture()
			in := setup(f)
			in.IdempotencyKey = "key-validation"
			if _, _, err := f.uc.Checkout(t.Context(), "buyer-1", in); err == nil {
				t.Fatal("expected an error")
			}
			if len(f.orders.byID) != 0 || len(f.inventory.reservedOrders) != 0 {
				t.Fatal("nothing may be created")
			}
		})
	}
}

func TestCheckout_PreviousOrderAwaitingCartConsumeBlocksDuplicate(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
	f.cart.consumeErr = apperror.Internal(errors.New("cart down"))

	order := f.checkout(t, "key-00000008")
	if task := f.consumptions.get(order.ID); task.Status != domain.CartConsumptionPending {
		t.Fatalf("expected a pending consume retry, got %+v", task)
	}
	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000009"})
	expectCode(t, err, apperror.CodeConflict)

	f.cart.consumeErr = nil
	if _, err := f.uc.ProcessCartConsumptions(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-00000010"}); err != nil {
		t.Fatalf("expected the buyer to shop again once the cart is consumed: %v", err)
	}
}

func TestPreview_QuotesPerShopAndReportsUnavailableShops(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.product("p2", "vendor-b", 2000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 2}, adapter.CartLine{ProductID: "p2", Quantity: 1})

	preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Ready || *preview.TotalAmount != 4000+40000 || len(preview.Vendors) != 2 {
		t.Fatalf("unexpected preview %+v", preview)
	}
	f.shipments.unavailable["vendor-b"] = true
	preview, err = f.uc.Preview(t.Context(), "buyer-1", f.addressID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ready || preview.TotalAmount != nil || preview.Vendors[1].ShippingError == "" {
		t.Fatalf("an unavailable quote must not be priced: %+v", preview)
	}
	if len(f.orders.byID) != 0 || len(f.checkoutOps.ops) != 0 {
		t.Fatal("a preview creates nothing")
	}
}

func TestRecoverCheckouts_FinishesOrUnwindsAbandonedCheckouts(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})

	// Simulate two requests that died after creating the order: one after
	// reserving, one before.
	build := func(key string, reserve bool) string {
		op, _, _ := f.checkoutOps.Begin(t.Context(), "buyer-1", key, "hash", time.Hour)
		plan, _ := domain.BuildCheckoutPlan("buyer-1", []domain.CheckoutLine{{ProductID: "p1", VendorID: "vendor-a", ProductName: "P", PriceAmount: 1000, Currency: "VND", Quantity: 1}})
		plan.CheckoutOperationID = op.ID
		order, _ := f.orders.CreateFromPlan(t.Context(), plan)
		if reserve {
			_ = f.inventory.Reserve(t.Context(), order.ID, []adapter.ReserveLine{{ProductID: "p1", Quantity: 1}})
		}
		f.checkoutOps.ops[op.ID].UpdatedAt = time.Now().Add(-time.Hour)
		return order.ID
	}
	reserved, orphan := build("key-reserved", true), build("key-orphan", false)

	if _, err := f.uc.RecoverCheckouts(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if o := f.orders.get(reserved); o.CheckoutState != domain.CheckoutReady || o.Status != domain.StatusPendingPayment {
		t.Errorf("an order with live stock must become payable, got %+v", o)
	}
	if o := f.orders.get(orphan); o.Status != domain.StatusCancelled || !f.inventory.releasedOrders[orphan] {
		t.Errorf("an order without stock must be cancelled with a release, got %+v", o)
	}
	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "key-orphan"})
	if err == nil {
		t.Fatal("the failed operation's key must replay its failure")
	}
}

func TestGetForPayment_RefusesAnOrderStillBeingPrepared(t *testing.T) {
	f := newCheckoutFixture()
	plan, _ := domain.BuildCheckoutPlan("buyer-1", []domain.CheckoutLine{{ProductID: "p1", VendorID: "vendor-a", ProductName: "P", PriceAmount: 1000, Currency: "VND", Quantity: 1}})
	order, _ := f.orders.CreateFromPlan(t.Context(), plan)
	_, err := f.uc.GetForPayment(t.Context(), order.ID)
	expectCode(t, err, domain.CodeOrderNotReady)
}

func TestCancel_BuyerCancelsOwnUnpaidOrderDurably(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p1", "vendor-a", 1000)
	f.cartOf(adapter.CartLine{ProductID: "p1", Quantity: 1})
	order := f.checkout(t, "key-00000011")

	_, err := f.uc.Cancel(t.Context(), "buyer-2", order.ID)
	expectCode(t, err, apperror.CodeForbidden)

	f.inventory.releaseError = apperror.Internal(errors.New("inventory down"))
	cancelled, err := f.uc.Cancel(t.Context(), "buyer-1", order.ID)
	if err != nil || cancelled.Status != domain.StatusCancelled {
		t.Fatalf("an Inventory outage must not block cancellation: %v", err)
	}
	if f.effects.count(domain.EffectReleaseInventory, domain.EffectPending) != 1 {
		t.Fatal("the release must stay queued for retry")
	}
	f.inventory.releaseError = nil
	if _, err := f.uc.ProcessEffects(t.Context(), "", 20); err != nil {
		t.Fatal(err)
	}
	if !f.inventory.releasedOrders[order.ID] || f.effects.count(domain.EffectReleaseInventory, domain.EffectDone) != 1 {
		t.Fatal("the worker must release the stock")
	}
	if _, err := f.uc.Cancel(t.Context(), "buyer-1", order.ID); err == nil {
		t.Fatal("a cancelled order cannot be cancelled again")
	}
}
