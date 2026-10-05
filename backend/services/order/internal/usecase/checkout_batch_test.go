package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

type quoteGateway struct {
	*fakeShipmentGateway
	quote func(context.Context, string, string, int64) (*domain.ShippingQuote, error)
}

func (g quoteGateway) Quote(ctx context.Context, id, province string, weight int64) (*domain.ShippingQuote, error) {
	return g.quote(ctx, id, province, weight)
}

func TestCheckoutBatchRetainsValidationAndCreatesNoOrderOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*checkoutFixture)
		code   apperror.Code
	}{
		{"missing_product", func(f *checkoutFixture) { delete(f.catalog.products, "p") }, apperror.CodeValidation},
		{"not_visible", func(f *checkoutFixture) { f.catalog.products["p"].IsVisible = false }, apperror.CodeValidation},
		{"requires_variant", func(f *checkoutFixture) { f.catalog.products["p"].HasVariants = true }, apperror.CodeValidation},
		{"missing_variant", func(f *checkoutFixture) {
			v := "gone"
			f.cartOf(adapter.CartLine{ProductID: "p", VariantID: &v, Quantity: 1})
		}, apperror.CodeValidation},
		{"wrong_product_variant", func(f *checkoutFixture) {
			v := "v"
			f.catalog.variants[v] = &adapter.VariantInfo{ID: v, ProductID: "other", SKU: "s"}
			f.cartOf(adapter.CartLine{ProductID: "p", VariantID: &v, Quantity: 1})
		}, apperror.CodeValidation},
		{"stale_price", func(f *checkoutFixture) {
			p, c := int64(1), "VND"
			f.cartOf(adapter.CartLine{ProductID: "p", Quantity: 1, SeenPriceAmount: &p, SeenCurrency: &c})
		}, domain.CodeCartChanged},
		{"weight_overflow", func(f *checkoutFixture) {
			w := int64(math.MaxInt64)
			f.catalog.products["p"].PackageWeightGrams = &w
			f.cartOf(adapter.CartLine{ProductID: "p", Quantity: 2})
		}, apperror.CodeValidation},
		{"catalog_outage", func(f *checkoutFixture) {
			f.catalog.getProduct = func(string) (*adapter.ProductInfo, error) {
				return nil, apperror.Internal(errors.New("test catalog outage"))
			}
		}, apperror.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCheckoutFixture()
			f.product("p", "shop", 100)
			f.cartOf(adapter.CartLine{ProductID: "p", Quantity: 1})
			tc.mutate(f)
			_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "batch-validation"})
			expectCode(t, err, tc.code)
			if f.catalog.batchCalls != 1 || len(f.orders.byID) != 0 || len(f.inventory.reservedOrders) != 0 {
				t.Fatal("failed validation caused writes or repeated batch")
			}
		})
	}
}

func TestCheckoutAndPreviewUseFreshBatchAndPreserveVariantSnapshots(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p", "shop", 100).HasVariants = true
	a, b := "a", "b"
	f.catalog.variants[a] = &adapter.VariantInfo{ID: a, ProductID: "p", SKU: "A", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "L"}}}
	f.catalog.variants[b] = &adapter.VariantInfo{ID: b, ProductID: "p", SKU: "B", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "M"}}}
	f.cartOf(adapter.CartLine{ProductID: "p", VariantID: &a, Quantity: 2}, adapter.CartLine{ProductID: "p", VariantID: &b, Quantity: 3})
	preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
	if err != nil || !preview.Ready || *preview.TotalAmount != 20500 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	f.shipments.fee = 30000 // no reuse of the preview quote
	_, _, err = f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, ExpectedTotal: preview.TotalAmount, IdempotencyKey: "batch-stale-total"})
	expectCode(t, err, domain.CodeCheckoutTotalChanged)
	order := f.checkout(t, "batch-current-total")
	items, _ := f.orders.ListItemsByOrder(t.Context(), order.ID)
	if f.catalog.batchCalls != 3 || order.SubtotalAmount != 500 || order.ShippingAmount != 30000 || len(items) != 2 || *items[0].VariantSKU != "A" || *items[1].VariantSKU != "B" {
		t.Fatalf("snapshots changed: %+v %+v", order, items)
	}
}

func TestShippingFanoutIsBoundedAndCancellationDrainsWorkers(t *testing.T) {
	f := newCheckoutFixture()
	lines := []adapter.CartLine{}
	for i := range 10 {
		id := fmt.Sprintf("p%d", i)
		f.product(id, fmt.Sprintf("s%d", i), 100)
		lines = append(lines, adapter.CartLine{ProductID: id, Quantity: 1})
	}
	f.cartOf(lines...)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{}, 10)
	var calls, active atomic.Int64
	f.uc.Shipments = quoteGateway{f.shipments, func(ctx context.Context, _ string, _ string, _ int64) (*domain.ShippingQuote, error) {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { _, err := f.uc.Preview(ctx, "buyer-1", f.addressID); done <- err }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("quotes did not run concurrently")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled checkout must fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("quote workers leaked")
	}
	if calls.Load() != 4 || active.Load() != 0 {
		t.Fatalf("calls=%d active=%d", calls.Load(), active.Load())
	}
}

func TestShippingResultsAndErrorsDoNotDependOnCompletionOrder(t *testing.T) {
	for _, infrastructure := range []bool{false, true} {
		f := newCheckoutFixture()
		f.product("pa", "a", 100)
		f.product("pb", "b", 200)
		f.cartOf(adapter.CartLine{ProductID: "pa", Quantity: 1}, adapter.CartLine{ProductID: "pb", Quantity: 2})
		bDone := make(chan struct{})
		f.uc.Shipments = quoteGateway{f.shipments, func(ctx context.Context, id, _ string, weight int64) (*domain.ShippingQuote, error) {
			if id == "a" {
				select {
				case <-bDone:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			} else {
				close(bDone)
			}
			if infrastructure {
				return nil, apperror.Conflict("quote failure " + id)
			}
			if id == "a" {
				return nil, domain.ShippingUnavailable("shop a unavailable")
			}
			return f.shipments.Quote(ctx, id, "HN", weight)
		}}
		preview, err := f.uc.Preview(t.Context(), "buyer-1", f.addressID)
		if infrastructure {
			if err == nil || mustAppError(t, err).Message != "quote failure a" {
				t.Fatalf("unstable error: %v", err)
			}
			continue
		}
		if err != nil || preview.Ready || preview.TotalAmount != nil || preview.Vendors[0].ShippingError != "shop a unavailable" || *preview.Vendors[1].ShippingFeeAmount != 20000 {
			t.Fatalf("partial quote: %+v %v", preview, err)
		}
	}
}

func TestMissingWeightNeverCallsShipmentAndExpiredQuoteNeverCreatesOrder(t *testing.T) {
	f := newCheckoutFixture()
	f.product("p", "s", 100).PackageWeightGrams = nil
	f.cartOf(adapter.CartLine{ProductID: "p", Quantity: 1})
	var calls int
	f.uc.Shipments = quoteGateway{f.shipments, func(ctx context.Context, id, province string, weight int64) (*domain.ShippingQuote, error) {
		calls++
		q, _ := f.shipments.Quote(ctx, id, province, weight)
		q.ExpiresAt = time.Now().Add(-time.Second)
		return q, nil
	}}
	_, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "missing-weight-key"})
	expectCode(t, err, domain.CodeShippingUnavailable)
	if calls != 0 {
		t.Fatal("guessed missing weight")
	}
	w := int64(100)
	f.catalog.products["p"].PackageWeightGrams = &w
	_, _, err = f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: "expired-quote-key"})
	expectCode(t, err, domain.CodeShippingUnavailable)
	if calls != 1 || len(f.orders.byID) != 0 {
		t.Fatal("expired quote accepted")
	}
}
