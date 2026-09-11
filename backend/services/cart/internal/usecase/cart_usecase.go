// Package usecase orchestrates Cart's workflows: adding/updating/removing
// items (always validated against Catalog, never trusting a client-sent
// price) and building the enriched view a buyer sees.
package usecase

import (
	"context"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/domain"
)

type CartUseCase struct {
	carts   CartRepositoryPort
	items   CartItemRepositoryPort
	catalog CatalogGateway
}

func NewCartUseCase(carts CartRepositoryPort, items CartItemRepositoryPort, catalog CatalogGateway) *CartUseCase {
	return &CartUseCase{carts: carts, items: items, catalog: catalog}
}

// LineView is one cart line enriched with live product data. Available is
// false when the product has since been deactivated, rejected, or deleted
// from the vendor's catalog — the line stays in the cart (so the buyer
// isn't silently robbed of their selection) but can't be checked out.
type LineView struct {
	ProductID    string
	ProductName  string
	VariantID    *string
	VariantSKU   *string
	VariantLabel *string
	Quantity     int64
	PriceAmount  int64
	Currency     string
	Available    bool
}

// AddItem adds quantity of a product (or, if variantID is given, one
// specific variant of it) to the buyer's cart. A product with variants
// must have one selected — there's no product-level stock to reserve for
// it once it has any.
func (uc *CartUseCase) AddItem(ctx context.Context, userID, productID string, variantID *string, quantity int64) error {
	if err := domain.ValidateQuantity(quantity); err != nil {
		return err
	}
	if err := uc.assertSellable(ctx, productID, variantID); err != nil {
		return err
	}

	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return apperror.Internal(err)
	}

	if variantID != nil {
		if err := uc.items.AddQuantityForVariant(ctx, cart.ID, productID, *variantID, quantity); err != nil {
			return apperror.Internal(err)
		}
		return nil
	}
	if err := uc.items.AddQuantity(ctx, cart.ID, productID, quantity); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *CartUseCase) SetItemQuantity(ctx context.Context, userID, productID string, variantID *string, quantity int64) error {
	if quantity < 0 {
		return apperror.Validation("Quantity cannot be negative")
	}
	if quantity > 0 {
		if err := uc.assertSellable(ctx, productID, variantID); err != nil {
			return err
		}
	}

	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return apperror.Internal(err)
	}

	if variantID != nil {
		if err := uc.items.SetQuantityForVariant(ctx, cart.ID, productID, *variantID, quantity); err != nil {
			return apperror.Internal(err)
		}
		return nil
	}
	if err := uc.items.SetQuantity(ctx, cart.ID, productID, quantity); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *CartUseCase) RemoveItem(ctx context.Context, userID, productID string, variantID *string) error {
	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return apperror.Internal(err)
	}

	if variantID != nil {
		if err := uc.items.RemoveVariant(ctx, cart.ID, *variantID); err != nil {
			return apperror.Internal(err)
		}
		return nil
	}
	if err := uc.items.Remove(ctx, cart.ID, productID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *CartUseCase) Clear(ctx context.Context, userID string) error {
	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return apperror.Internal(err)
	}

	if err := uc.items.Clear(ctx, cart.ID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

func (uc *CartUseCase) View(ctx context.Context, userID string) ([]LineView, error) {
	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	items, err := uc.items.ListByCart(ctx, cart.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	views := make([]LineView, 0, len(items))
	for _, item := range items {
		product, err := uc.catalog.GetProduct(ctx, item.ProductID)
		if err != nil {
			// A product that vanished from Catalog entirely still shows as
			// an unavailable line, not a broken cart page.
			views = append(views, LineView{ProductID: item.ProductID, VariantID: item.VariantID, Quantity: item.Quantity, Available: false})
			continue
		}

		view := LineView{
			ProductID:   product.ID,
			ProductName: product.Name,
			Quantity:    item.Quantity,
			PriceAmount: product.PriceAmount,
			Currency:    product.Currency,
			Available:   product.IsVisible,
		}
		if item.VariantID != nil {
			view.VariantID = item.VariantID
			if variant, err := uc.catalog.GetVariant(ctx, *item.VariantID); err == nil {
				view.VariantSKU = &variant.SKU
				label := variantLabel(variant.Options)
				view.VariantLabel = &label
			}
			// A variant lookup failure doesn't hide the line or force it
			// unavailable — the product itself is still the source of
			// truth for Available; the label is purely cosmetic.
		}
		views = append(views, view)
	}
	return views, nil
}

// assertSellable checks the product is visible, and — if a variant was
// given — that it actually belongs to this product. A product that
// HasVariants but got no variantID is rejected: once a product has any
// variant, there's no product-level stock left to reserve against.
func (uc *CartUseCase) assertSellable(ctx context.Context, productID string, variantID *string) error {
	product, err := uc.catalog.GetProduct(ctx, productID)
	if err != nil {
		return err
	}
	if !product.IsVisible {
		return apperror.Validation("This product is not currently available for purchase")
	}
	if product.HasVariants && variantID == nil {
		return apperror.Validation("Please select a product option before adding to cart")
	}
	if variantID != nil {
		variant, err := uc.catalog.GetVariant(ctx, *variantID)
		if err != nil {
			return err
		}
		if variant.ProductID != productID {
			return apperror.Validation("This option does not belong to the selected product")
		}
	}
	return nil
}

// variantLabel joins a variant's option labels into one display string,
// e.g. "Color: Red, Size: L".
func variantLabel(options []adapter.VariantOptionInfo) string {
	parts := make([]string, 0, len(options))
	for _, o := range options {
		parts = append(parts, o.AttributeName+": "+o.OptionValue)
	}
	return strings.Join(parts, ", ")
}
