// Package usecase orchestrates Cart's workflows: buyer mutations (always
// validated against Catalog, never trusting a client-sent price, serialized
// per cart and optionally conditional on the cart version), the enriched
// view a buyer sees, and the checkout snapshot/consume contract Order uses.
package usecase

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/purchasequote"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

type CartUseCase struct {
	SelectiveCheckoutEnabled bool
	tx                       TxRunner
	carts                    CartRepositoryPort
	items                    CartItemRepositoryPort
	operations               CheckoutOperationRepositoryPort
	catalog                  CatalogGateway
	inventory                InventoryGateway
	log                      zerolog.Logger
}

func NewCartUseCase(
	tx TxRunner,
	carts CartRepositoryPort,
	items CartItemRepositoryPort,
	operations CheckoutOperationRepositoryPort,
	catalog CatalogGateway,
	inventory InventoryGateway,
	log zerolog.Logger,
) *CartUseCase {
	return &CartUseCase{tx: tx, carts: carts, items: items, operations: operations, catalog: catalog, inventory: inventory, log: log}
}

// AddItem adds quantity of a product (or, if variantID is given, one
// specific variant of it) to the buyer's cart. The price the buyer is
// looking at right now becomes the line's price reference.
func (uc *CartUseCase) AddItem(ctx context.Context, userID, productID string, variantID *string, quantity int64, expectedVersion *int64) error {
	if err := domain.ValidateQuantity(quantity); err != nil {
		return err
	}
	product, err := uc.assertSellable(ctx, productID, variantID)
	if err != nil {
		return err
	}

	return uc.mutate(ctx, userID, expectedVersion, func(ctx context.Context, cart *domain.Cart) (bool, error) {
		line, err := uc.items.FindLine(ctx, cart.ID, productID, variantID)
		if err != nil {
			return false, err
		}
		if line == nil {
			return true, uc.insertLine(ctx, cart, productID, variantID, quantity, product)
		}
		next, err := domain.AccumulatedQuantity(line.Quantity, quantity)
		if err != nil {
			return false, err
		}
		if err := uc.ensureSingleCurrency(ctx, cart.ID, product.Currency, &line.ID); err != nil {
			return false, err
		}
		line.Quantity = next
		return true, uc.items.Update(ctx, line)
	})
}

// SetItemQuantity sets an absolute quantity; 0 removes the line. Changing
// the quantity alone does not count as accepting a new price: a line whose
// price moved stays flagged until the buyer confirms it.
func (uc *CartUseCase) SetItemQuantity(ctx context.Context, userID, productID string, variantID *string, quantity int64, expectedVersion *int64) error {
	if err := domain.ValidateSetQuantity(quantity); err != nil {
		return err
	}
	if quantity == 0 {
		return uc.RemoveItem(ctx, userID, productID, variantID, expectedVersion)
	}
	product, err := uc.assertSellable(ctx, productID, variantID)
	if err != nil {
		return err
	}

	return uc.mutate(ctx, userID, expectedVersion, func(ctx context.Context, cart *domain.Cart) (bool, error) {
		line, err := uc.items.FindLine(ctx, cart.ID, productID, variantID)
		if err != nil {
			return false, err
		}
		if line == nil {
			return true, uc.insertLine(ctx, cart, productID, variantID, quantity, product)
		}
		line.Quantity = quantity
		return true, uc.items.Update(ctx, line)
	})
}

// RemoveItem is idempotent: removing a line that is not there succeeds
// without changing the cart version.
func (uc *CartUseCase) RemoveItem(ctx context.Context, userID, productID string, variantID *string, expectedVersion *int64) error {
	return uc.mutate(ctx, userID, expectedVersion, func(ctx context.Context, cart *domain.Cart) (bool, error) {
		line, err := uc.items.FindLine(ctx, cart.ID, productID, variantID)
		if err != nil || line == nil {
			return false, err
		}
		return uc.items.Delete(ctx, cart.ID, line.ID)
	})
}

// Clear empties the buyer's own cart. Order no longer uses this after
// checkout (it consumes only the purchased lines); it stays for the buyer
// and for legacy Order builds during cutover.
func (uc *CartUseCase) Clear(ctx context.Context, userID string, expectedVersion *int64) error {
	return uc.mutate(ctx, userID, expectedVersion, func(ctx context.Context, cart *domain.Cart) (bool, error) {
		n, err := uc.items.DeleteAll(ctx, cart.ID)
		return n > 0, err
	})
}

// PriceConfirmation is the price the buyer saw for a line when they
// accepted its new price.
type PriceConfirmation struct {
	VariantID      *string
	PricingVersion int64
	LineID         string
	PriceAmount    int64
	Currency       string
}

// ConfirmPrices records that the buyer has seen and accepted the current
// price of the given lines. It only succeeds when the confirmed price is
// still Catalog's live price — a price that moved again must be reviewed
// again — and when the cart is still at expectedVersion.
func (uc *CartUseCase) ConfirmPrices(ctx context.Context, userID string, expectedVersion int64, confirmations []PriceConfirmation) error {
	if len(confirmations) == 0 {
		return apperror.Validation("At least one line is required")
	}
	if len(confirmations) > domain.MaxLinesPerCart {
		return apperror.Validation("Too many lines")
	}
	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return apperror.Internal(err)
	}
	ids := make([]string, 0, len(confirmations))
	for _, c := range confirmations {
		ids = append(ids, c.LineID)
	}
	lines, err := uc.items.ListByIDs(ctx, cart.ID, ids)
	if err != nil {
		return apperror.Internal(err)
	}
	byID := make(map[string]*domain.CartItem, len(lines))
	for _, l := range lines {
		byID[l.ID] = l
	}
	inputs := make([]purchasequote.Input, 0, len(confirmations))
	seen := map[string]bool{}
	for _, c := range confirmations {
		line := byID[c.LineID]
		if line == nil || seen[c.LineID] {
			return domain.CartChanged("Invalid price confirmation")
		}
		seen[c.LineID] = true
		if purchasequote.Key(line.ProductID, line.VariantID) != purchasequote.Key(line.ProductID, c.VariantID) {
			return domain.CartChanged("The selected option changed")
		}
		inputs = append(inputs, purchasequote.Input{ProductID: line.ProductID, VariantID: line.VariantID, Quantity: line.Quantity})
	}
	quote, err := uc.catalog.PurchaseQuotes(ctx, inputs)
	if err != nil {
		return err
	}
	for i, c := range confirmations {
		q := quote.Lines[i]
		if !q.IsSellable {
			return purchasequote.Unavailable()
		}
		if q.UnitPrice != c.PriceAmount || q.Currency != c.Currency || (q.VariantID != nil && q.VariantVersion != c.PricingVersion) {
			return purchasequote.PriceChanged()
		}
	}

	return uc.mutate(ctx, userID, &expectedVersion, func(ctx context.Context, cart *domain.Cart) (bool, error) {
		current, err := uc.items.ListByIDs(ctx, cart.ID, ids)
		if err != nil {
			return false, err
		}
		if len(current) != len(confirmations) {
			return false, domain.CartChanged("An item in your cart changed. Please review it again.")
		}
		confirmed := make(map[string]PriceConfirmation, len(confirmations))
		for _, c := range confirmations {
			confirmed[c.LineID] = c
		}
		for _, line := range current {
			c := confirmed[line.ID]
			amount, currency := c.PriceAmount, c.Currency
			line.SeenPriceAmount, line.SeenCurrency = &amount, &currency
			if line.VariantID != nil {
				version := c.PricingVersion
				line.SeenPricingVersion = &version
			}
			if err := uc.items.Update(ctx, line); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}

// mutate runs fn with the buyer's cart locked, after checking the optional
// expected version, and bumps the version when fn reports a change.
func (uc *CartUseCase) mutate(ctx context.Context, userID string, expectedVersion *int64, fn func(context.Context, *domain.Cart) (bool, error)) error {
	err := uc.tx.Run(ctx, func(ctx context.Context) error {
		cart, err := uc.carts.LockForUser(ctx, userID)
		if err != nil {
			return err
		}
		if err := domain.CheckExpectedVersion(expectedVersion, cart.Version); err != nil {
			return err
		}
		changed, err := fn(ctx, cart)
		if err != nil || !changed {
			return err
		}
		_, err = uc.carts.BumpVersion(ctx, cart.ID)
		return err
	})
	return asAppError(err)
}

func (uc *CartUseCase) insertLine(ctx context.Context, cart *domain.Cart, productID string, variantID *string, quantity int64, product *adapter.ProductInfo) error {
	count, err := uc.items.CountLines(ctx, cart.ID)
	if err != nil {
		return err
	}
	if err := domain.EnsureLineCapacity(count); err != nil {
		return err
	}
	if err := uc.ensureSingleCurrency(ctx, cart.ID, product.Currency, nil); err != nil {
		return err
	}
	item := &domain.CartItem{CartID: cart.ID, ProductID: productID, VariantID: variantID, Quantity: quantity}
	item.SeenPriceAmount, item.SeenCurrency = priceReference(product)
	if variantID != nil {
		version := product.PricingVersion
		item.SeenPricingVersion = &version
	}
	return uc.items.Insert(ctx, item)
}

func (uc *CartUseCase) ensureSingleCurrency(ctx context.Context, cartID, currency string, exceptLineID *string) error {
	other, err := uc.items.HasOtherCurrency(ctx, cartID, currency, exceptLineID)
	if err != nil {
		return err
	}
	if other {
		return apperror.Validation("Your cart already has items priced in a different currency; please check them out or remove them first")
	}
	return nil
}

// assertSellable checks the product is visible, and — if a variant was
// given — that it actually belongs to this product. A product that
// HasVariants but got no variantID is rejected: once a product has any
// variant, there's no product-level stock left to reserve against.
func (uc *CartUseCase) assertSellable(ctx context.Context, productID string, variantID *string) (*adapter.ProductInfo, error) {
	quote, err := uc.catalog.PurchaseQuotes(ctx, []purchasequote.Input{{ProductID: productID, VariantID: variantID, Quantity: 1}})
	if err != nil {
		return nil, err
	}
	q := quote.Lines[0]
	if !q.IsSellable {
		if q.ShopPaused {
			return nil, vendorsales.NotAcceptingOrders()
		}
		if variantID == nil {
			return nil, apperror.Validation("This product is not currently available; select an option if required")
		}
		return nil, purchasequote.Unavailable()
	}
	return &adapter.ProductInfo{ID: q.ProductID, VendorID: q.VendorID, Name: q.ProductName, PriceAmount: q.UnitPrice, Currency: q.Currency, Status: q.Status, IsVisible: q.IsSellable, HasVariants: q.HasVariants, PricingVersion: q.VariantVersion}, nil
}

func priceReference(product *adapter.ProductInfo) (*int64, *string) {
	amount, currency := product.PriceAmount, product.Currency
	return &amount, &currency
}

// asAppError keeps business errors as they are and wraps everything else
// (database, transaction) as an internal error that is logged, not shown.
func asAppError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return apperror.Internal(err)
}
