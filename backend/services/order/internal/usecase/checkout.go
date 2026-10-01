package usecase

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
)

// CheckoutInput is the buyer's checkout request. IdempotencyKey identifies
// the attempt (clients send a new key per confirmed order); CartVersion and
// ExpectedTotal are what the buyer reviewed.
type CheckoutInput struct {
	AddressID      string
	CartVersion    *int64
	ExpectedTotal  *int64
	IdempotencyKey string
}

// Checkout places an order exactly once per (buyer, idempotency key):
//  1. claim the key; a retry of the same request returns the first outcome,
//     a different request with the same key is refused,
//  2. refuse while an earlier order still waits for its cart lines to be
//     consumed, so a refresh cannot buy the same cart twice,
//  3. snapshot the cart, re-price every line against Catalog, quote shipping
//     per vendor with Shipment (a failed quote fails checkout; shipping is
//     never assumed free) and snapshot the current commission rule,
//  4. refuse if the total differs from the one the buyer confirmed,
//  5. persist order, vendor orders, items, cart-consume task and the link
//     to this checkout in one transaction, in state "preparing",
//  6. reserve stock (idempotent per order); on failure cancel the order
//     durably, on success mark it ready — only then can Payment open an
//     intent — and complete the checkout operation in the same transaction.
//
// replayed is true when an earlier attempt's order is returned.
func (uc *OrderUseCase) Checkout(ctx context.Context, buyerID string, in CheckoutInput) (order *domain.Order, replayed bool, err error) {
	key := in.IdempotencyKey
	if key == "" {
		key = uuid.NewString() // legacy clients: no retry protection
	} else if err := domain.ValidateIdempotencyKey(key); err != nil {
		return nil, false, err
	}
	hash := domain.CheckoutRequest{AddressID: in.AddressID, CartVersion: in.CartVersion, ExpectedTotal: in.ExpectedTotal}.Hash()

	op, started, err := uc.CheckoutOps.Begin(ctx, buyerID, key, hash, domain.CheckoutKeyTTL)
	if err != nil {
		return nil, false, appError(err)
	}
	if !started {
		order, err := uc.replayCheckout(ctx, buyerID, op, hash)
		return order, err == nil, err
	}

	order, err = uc.runCheckout(ctx, buyerID, op, in)
	if err != nil {
		cause := appError(err)
		if failErr := uc.CheckoutOps.Fail(context.WithoutCancel(ctx), op.ID, cause); failErr != nil {
			uc.Log.Error().Err(failErr).Str("checkout_operation_id", op.ID).Msg("order_checkout_fail_record_failed")
		}
		return nil, false, cause
	}
	return order, false, nil
}

func (uc *OrderUseCase) replayCheckout(ctx context.Context, buyerID string, op *domain.CheckoutOperation, hash string) (*domain.Order, error) {
	if op.RequestHash != hash {
		return nil, domain.IdempotencyKeyReused()
	}
	switch op.Status {
	case domain.CheckoutOpCompleted:
		if op.OrderID == nil {
			return nil, apperror.Internal(errors.New("completed checkout without an order"))
		}
		return uc.findOwnedByBuyer(ctx, buyerID, *op.OrderID)
	case domain.CheckoutOpFailed:
		return nil, op.StoredError()
	default:
		return nil, domain.CheckoutInProgress()
	}
}

func (uc *OrderUseCase) runCheckout(ctx context.Context, buyerID string, op *domain.CheckoutOperation, in CheckoutInput) (*domain.Order, error) {
	if err := uc.settleOpenCartConsumptions(ctx, buyerID); err != nil {
		return nil, err
	}

	cartOperationID := uuid.NewString()
	snapshot, err := uc.Cart.Snapshot(ctx, buyerID, cartOperationID, in.CartVersion)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Lines) == 0 {
		return nil, apperror.Validation("Your cart is empty")
	}
	address, err := uc.resolveCheckoutAddress(ctx, buyerID, in.AddressID)
	if err != nil {
		return nil, err
	}

	priced, err := uc.priceLines(ctx, snapshot.Lines)
	if err != nil {
		return nil, err
	}
	plan, err := domain.BuildCheckoutPlan(buyerID, priced.lines)
	if err != nil {
		return nil, err
	}
	quotes, failures, err := uc.quoteShipping(ctx, plan.VendorIDs(), address.Province, priced)
	if err != nil {
		return nil, err
	}
	if failure := firstFailure(plan.VendorIDs(), failures); failure != nil {
		return nil, failure
	}
	if err := plan.ApplyShipping(quotes); err != nil {
		return nil, err
	}
	rule, err := uc.CommissionRules.FindCurrent(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if err := plan.ApplyCommission(rule); err != nil {
		return nil, err
	}
	if in.ExpectedTotal != nil && *in.ExpectedTotal != plan.Order.TotalAmount {
		return nil, domain.CheckoutTotalChanged()
	}

	plan.ProductVersions = priced.productVersions
	consumeLines := make([]domain.CartConsumeLine, 0, len(snapshot.Lines))
	for _, line := range snapshot.Lines {
		consumeLines = append(consumeLines, domain.CartConsumeLine{LineID: line.LineID, Quantity: line.Quantity})
	}
	plan.CartConsumption = &domain.CartConsumption{BuyerID: buyerID, OperationID: cartOperationID, Lines: consumeLines}
	plan.CheckoutOperationID = op.ID
	plan.Order.RecipientName, plan.Order.Phone = address.RecipientName, address.Phone
	plan.Order.Province, plan.Order.District, plan.Order.Ward, plan.Order.StreetAddress =
		address.Province, address.District, address.Ward, address.StreetAddress
	if plan.VendorVersions, err = uc.Vendors.Approved(ctx, plan.VendorIDs()); err != nil {
		return nil, err
	}

	order, err := uc.Orders.CreateFromPlan(ctx, plan)
	if err != nil {
		return nil, appError(err)
	}

	reserveLines := make([]adapter.ReserveLine, 0, len(priced.lines))
	for _, line := range priced.lines {
		reserveLines = append(reserveLines, adapter.ReserveLine{ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity})
	}
	if reserveErr := uc.Inventory.Reserve(ctx, order.ID, reserveLines); reserveErr != nil {
		uc.abandonCheckout(ctx, order.ID, reserveErr)
		return nil, reserveErr
	}

	err = uc.withOrder(ctx, order.ID, func(ctx context.Context) error {
		if err := uc.Orders.SetCheckoutState(ctx, order.ID, domain.CheckoutReady); err != nil {
			return err
		}
		return uc.CheckoutOps.Complete(ctx, op.ID, order.ID)
	})
	if err != nil {
		// The order and its reservation exist; recovery finishes the
		// checkout from the reservation state.
		uc.Log.Error().Err(err).Str("order_id", order.ID).Msg("order_checkout_ready_failed")
		return nil, err
	}
	order.CheckoutState = domain.CheckoutReady

	if err := uc.CartConsumption.Activate(ctx, order.ID); err != nil {
		uc.Log.Error().Err(err).Str("order_id", order.ID).Msg("order_cart_consume_activate_failed")
		return order, nil
	}
	plan.CartConsumption.OrderID = order.ID
	uc.consumeCart(ctx, plan.CartConsumption)
	return order, nil
}

// abandonCheckout cancels an order whose stock could not be reserved: the
// release (a tombstone, so a late reserve cannot hold stock) and the cart
// task cancellation are durable, and tried once right away.
func (uc *OrderUseCase) abandonCheckout(ctx context.Context, orderID string, cause error) {
	ctx = context.WithoutCancel(ctx)
	reason := "Stock reservation failed at checkout"
	var app *apperror.Error
	if errors.As(cause, &app) && app.Code != apperror.CodeInternal {
		reason = app.Message
	}
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		order, err := uc.findOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if order.Status == domain.StatusCancelled {
			return nil
		}
		return uc.cancelLocked(ctx, order, reason, false)
	})
	if err != nil {
		uc.Log.Error().Err(err).Str("order_id", orderID).Msg("order_checkout_abandon_failed")
	}
	if err := uc.CartConsumption.Cancel(ctx, orderID); err != nil {
		uc.Log.Error().Err(err).Str("order_id", orderID).Msg("order_cart_consume_cancel_failed")
	}
	uc.runEffectsSoon(ctx, orderID)
}

// resolveCheckoutAddress looks up and verifies ownership of the buyer's
// chosen address.
func (uc *OrderUseCase) resolveCheckoutAddress(ctx context.Context, buyerID, addressID string) (*domain.BuyerAddress, error) {
	if addressID == "" {
		return nil, apperror.Validation("Please choose a shipping address before checking out")
	}
	address, err := uc.BuyerAddresses.FindByID(ctx, addressID)
	if err != nil {
		return nil, apperror.Validation("Please choose a valid shipping address before checking out")
	}
	if address.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this address")
	}
	return address, nil
}

type pricedLines struct {
	lines           []domain.CheckoutLine
	weightByVendor  map[string]int64
	productVersions map[string]int64
	// missingWeight: a vendor has a product without package weight; its
	// shipping cannot be priced.
	missingWeight map[string]bool
}

// priceLines re-prices and validates every cart line against Catalog now,
// and refuses a price the buyer has not accepted in the cart.
func (uc *OrderUseCase) priceLines(ctx context.Context, cartLines []adapter.CartLine) (*pricedLines, error) {
	out := &pricedLines{weightByVendor: map[string]int64{}, productVersions: map[string]int64{}, missingWeight: map[string]bool{}}
	for _, line := range cartLines {
		product, err := uc.Catalog.GetProduct(ctx, line.ProductID)
		if err != nil {
			var appErr *apperror.Error
			if errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound {
				return nil, apperror.Validation("A product in your cart no longer exists; please update your cart")
			}
			return nil, err
		}
		if !product.IsVisible {
			return nil, apperror.Validation("\"" + product.Name + "\" is no longer available; please update your cart")
		}
		if product.HasVariants && line.VariantID == nil {
			return nil, apperror.Validation("\"" + product.Name + "\" requires selecting an option; please update your cart")
		}
		if line.SeenPriceAmount != nil && line.SeenCurrency != nil &&
			(*line.SeenPriceAmount != product.PriceAmount || *line.SeenCurrency != product.Currency) {
			return nil, domain.CartChanged("The price of \"" + product.Name + "\" changed; please review your cart before checking out")
		}
		if previous, exists := out.productVersions[product.ID]; exists && previous != product.Version {
			return nil, apperror.Conflict("A product changed during checkout; please retry")
		}
		out.productVersions[product.ID] = product.Version

		checkoutLine := domain.CheckoutLine{ProductID: product.ID, VendorID: product.VendorID, ProductName: product.Name,
			PriceAmount: product.PriceAmount, Currency: product.Currency, Quantity: line.Quantity}
		if line.VariantID != nil {
			variant, err := uc.Catalog.GetVariant(ctx, *line.VariantID)
			if err != nil {
				var appErr *apperror.Error
				if errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound {
					return nil, apperror.Validation("A selected option in your cart no longer exists; please update your cart")
				}
				return nil, err
			}
			if variant.ProductID != product.ID {
				return nil, apperror.Validation("A selected option in your cart no longer matches its product; please update your cart")
			}
			label := variantLabel(variant.Options)
			checkoutLine.VariantID, checkoutLine.VariantSKU, checkoutLine.VariantLabel = line.VariantID, &variant.SKU, &label
		}
		out.lines = append(out.lines, checkoutLine)

		// A product without packaging data cannot be priced for shipping:
		// its shop is reported unavailable, never charged a guessed fee.
		if product.PackageWeightGrams == nil || *product.PackageWeightGrams <= 0 {
			out.missingWeight[product.VendorID] = true
		} else {
			weight, ok := domain.MulAmount(*product.PackageWeightGrams, line.Quantity)
			if !ok {
				return nil, apperror.Validation("Package weight is too large")
			}
			if out.weightByVendor[product.VendorID], ok = domain.AddAmount(out.weightByVendor[product.VendorID], weight); !ok {
				return nil, apperror.Validation("Package weight is too large")
			}
		}
	}
	return out, nil
}

// quoteShipping asks Shipment for one quote per vendor. A vendor Shipment
// cannot serve is reported in failures (shipping_unavailable); any other
// error aborts.
func (uc *OrderUseCase) quoteShipping(ctx context.Context, vendorIDs []string, province string, priced *pricedLines) (map[string]domain.ShippingQuote, map[string]*apperror.Error, error) {
	quotes := map[string]domain.ShippingQuote{}
	failures := map[string]*apperror.Error{}
	for _, vendorID := range vendorIDs {
		if priced.missingWeight[vendorID] {
			failures[vendorID] = domain.ShippingUnavailable("A product of this shop has no package weight yet, so shipping cannot be priced")
			continue
		}
		q, err := uc.Shipments.Quote(ctx, vendorID, province, priced.weightByVendor[vendorID])
		if err != nil {
			app := appError(err)
			if app.Code == domain.CodeShippingUnavailable {
				failures[vendorID] = app
				continue
			}
			return nil, nil, app
		}
		quotes[vendorID] = *q
	}
	return quotes, failures, nil
}

// firstFailure returns the first vendor's quote failure in plan order.
func firstFailure(vendorIDs []string, failures map[string]*apperror.Error) *apperror.Error {
	for _, id := range vendorIDs {
		if f, ok := failures[id]; ok {
			return f
		}
	}
	return nil
}

// CheckoutPreview is the order the buyer is about to confirm: current
// prices and a shipping quote per shop. Nothing is reserved or created.
type CheckoutPreview struct {
	CartVersion    int64
	Currency       string
	SubtotalAmount int64
	ShippingAmount *int64
	TotalAmount    *int64
	Vendors        []PreviewVendor
	Ready          bool
}

type PreviewVendor struct {
	VendorID          string
	SubtotalAmount    int64
	ShippingFeeAmount *int64
	ShippingError     string
	ItemCount         int64
}

// Preview prices the buyer's cart for an address. A shop Shipment cannot
// serve is reported per shop (Ready false), never priced at zero.
func (uc *OrderUseCase) Preview(ctx context.Context, buyerID, addressID string) (*CheckoutPreview, error) {
	snapshot, err := uc.Cart.Lines(ctx, buyerID)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Lines) == 0 {
		return nil, apperror.Validation("Your cart is empty")
	}
	address, err := uc.resolveCheckoutAddress(ctx, buyerID, addressID)
	if err != nil {
		return nil, err
	}
	priced, err := uc.priceLines(ctx, snapshot.Lines)
	if err != nil {
		return nil, err
	}
	plan, err := domain.BuildCheckoutPlan(buyerID, priced.lines)
	if err != nil {
		return nil, err
	}
	quotes, failures, err := uc.quoteShipping(ctx, plan.VendorIDs(), address.Province, priced)
	if err != nil {
		return nil, err
	}

	counts := map[string]int64{}
	for _, l := range priced.lines {
		counts[l.VendorID] += l.Quantity
	}
	preview := &CheckoutPreview{CartVersion: snapshot.CartVersion, Currency: plan.Order.Currency, SubtotalAmount: plan.Order.SubtotalAmount}
	for _, vo := range plan.VendorOrders {
		v := PreviewVendor{VendorID: vo.VendorID, SubtotalAmount: vo.SubtotalAmount, ItemCount: counts[vo.VendorID]}
		if q, ok := quotes[vo.VendorID]; ok {
			v.ShippingFeeAmount = ptr(q.FeeAmount)
		} else if f := failures[vo.VendorID]; f != nil {
			v.ShippingError = f.Message
		}
		preview.Vendors = append(preview.Vendors, v)
	}
	sort.SliceStable(preview.Vendors, func(i, j int) bool { return preview.Vendors[i].VendorID < preview.Vendors[j].VendorID })

	if len(failures) == 0 {
		if err := plan.ApplyShipping(quotes); err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code == domain.CodeShippingUnavailable {
				return preview, nil
			}
			return nil, err
		}
		preview.ShippingAmount, preview.TotalAmount, preview.Ready = ptr(plan.Order.ShippingAmount), ptr(plan.Order.TotalAmount), true
	}
	return preview, nil
}

// RecoverCheckouts finishes or unwinds checkouts whose request died
// mid-way. An order with live stock becomes ready; one without is
// cancelled (with a durable release) and the operation fails, so the buyer
// is never charged for an order that was never prepared.
func (uc *OrderUseCase) RecoverCheckouts(ctx context.Context, limit int) (int, error) {
	stale, err := uc.CheckoutOps.ListStalePreparing(ctx, uc.Now().Add(-domain.CheckoutStaleAfter), limit)
	if err != nil {
		return 0, err
	}
	for _, op := range stale {
		uc.recoverCheckout(ctx, op)
	}
	return len(stale), nil
}

func (uc *OrderUseCase) recoverCheckout(ctx context.Context, op *domain.CheckoutOperation) {
	logger := uc.Log.With().Str("checkout_operation_id", op.ID).Logger()
	notCompleted := apperror.Conflict("Checkout could not be completed and nothing was charged; please try again")
	if op.OrderID == nil {
		if err := uc.CheckoutOps.Fail(ctx, op.ID, apperror.Internal(errors.New("checkout request ended before the order was created"))); err != nil {
			logger.Error().Err(err).Msg("order_checkout_recovery_failed")
		}
		return
	}
	orderID := *op.OrderID
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		logger.Error().Err(err).Msg("order_checkout_recovery_failed")
		return
	}
	switch {
	case order.CheckoutState == domain.CheckoutReady:
		err = uc.CheckoutOps.Complete(ctx, op.ID, orderID)
	case order.Status == domain.StatusCancelled:
		err = uc.CheckoutOps.Fail(ctx, op.ID, notCompleted)
	default:
		receipt, receiptErr := uc.Inventory.Operation(ctx, orderID)
		var app *apperror.Error
		switch {
		case receiptErr == nil && (receipt.Status == "held" || receipt.Status == "committed"):
			err = uc.withOrder(ctx, orderID, func(ctx context.Context) error {
				if err := uc.Orders.SetCheckoutState(ctx, orderID, domain.CheckoutReady); err != nil {
					return err
				}
				return uc.CheckoutOps.Complete(ctx, op.ID, orderID)
			})
		case receiptErr == nil || (errors.As(receiptErr, &app) && app.Code == apperror.CodeNotFound):
			uc.abandonCheckout(ctx, orderID, apperror.Conflict("Checkout did not complete"))
			err = uc.CheckoutOps.Fail(ctx, op.ID, notCompleted)
		default:
			logger.Warn().Err(receiptErr).Msg("order_checkout_recovery_deferred")
			return
		}
	}
	if err != nil {
		logger.Error().Err(err).Msg("order_checkout_recovery_failed")
		return
	}
	logger.Info().Str("order_id", orderID).Msg("order_checkout_recovered")
}
