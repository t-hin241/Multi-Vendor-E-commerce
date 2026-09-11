package usecase_test

import (
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/cart/internal/adapter"
	"shopee/backend/services/cart/internal/usecase"
)

func newTestCartUseCase() (*usecase.CartUseCase, *fakeCatalogGateway) {
	catalog := newFakeCatalogGateway()
	uc := usecase.NewCartUseCase(newFakeCartRepository(), newFakeCartItemRepository(), catalog)
	return uc, catalog
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func TestAddItem_RejectsUnsellableProduct(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", IsVisible: false}

	err := uc.AddItem(t.Context(), "user-1", "product-1", nil, 2)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an unsellable product, got %v", appErr.Code)
	}
}

func TestAddItem_AccumulatesQuantity(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", Name: "Sneakers", PriceAmount: 100000, Currency: "VND", IsVisible: true}
	ctx := t.Context()

	if err := uc.AddItem(ctx, "user-1", "product-1", nil, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.AddItem(ctx, "user-1", "product-1", nil, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines, err := uc.View(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || lines[0].Quantity != 5 {
		t.Fatalf("expected a single line with quantity 5, got %+v", lines)
	}
}

func TestView_MarksVanishedProductAsUnavailable(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", IsVisible: true}
	ctx := t.Context()

	if err := uc.AddItem(ctx, "user-1", "product-1", nil, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The vendor deletes/deactivates the product entirely after it was added.
	delete(catalog.products, "product-1")

	lines, err := uc.View(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected the line to remain in the cart, got %d lines", len(lines))
	}
	if lines[0].Available {
		t.Error("expected the line to be marked unavailable")
	}
}

func TestView_ExcludesUnavailableLinesFromTotal(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", PriceAmount: 100, IsVisible: true}
	catalog.products["product-2"] = &adapter.ProductInfo{ID: "product-2", PriceAmount: 500, IsVisible: false}
	ctx := t.Context()

	if err := uc.AddItem(ctx, "user-1", "product-1", nil, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deactivate product-2 only after adding is impossible via AddItem (it
	// would be rejected), so seed it directly via SetItemQuantity's
	// zero-guard bypassed path: simulate by adding while visible, then
	// flipping visibility.
	catalog.products["product-2"].IsVisible = true
	if err := uc.AddItem(ctx, "user-1", "product-2", nil, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	catalog.products["product-2"].IsVisible = false

	lines, err := uc.View(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var total int64
	for _, l := range lines {
		if l.Available {
			total += l.PriceAmount * l.Quantity
		}
	}
	if total != 200 {
		t.Errorf("expected only the available line to count toward total, got %d", total)
	}
}

func TestSetItemQuantity_ZeroRemovesItem(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", IsVisible: true}
	ctx := t.Context()

	if err := uc.AddItem(ctx, "user-1", "product-1", nil, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.SetItemQuantity(ctx, "user-1", "product-1", nil, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines, err := uc.View(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected the cart to be empty, got %+v", lines)
	}
}

func TestRemoveItem_IsIdempotent(t *testing.T) {
	uc, _ := newTestCartUseCase()

	if err := uc.RemoveItem(t.Context(), "user-1", "product-never-added", nil); err != nil {
		t.Errorf("removing a never-added item must succeed, got: %v", err)
	}
}

func TestAddItem_RequiresVariantWhenProductHasVariants(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", IsVisible: true, HasVariants: true}

	err := uc.AddItem(t.Context(), "user-1", "product-1", nil, 1)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error when a variant-having product is added without one, got %v", appErr.Code)
	}
}

func TestAddItem_RejectsMismatchedVariant(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", IsVisible: true, HasVariants: true}
	catalog.variants["variant-1"] = &adapter.VariantInfo{ID: "variant-1", ProductID: "some-other-product"}

	variantID := "variant-1"
	err := uc.AddItem(t.Context(), "user-1", "product-1", &variantID, 1)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a variant that doesn't belong to the product, got %v", appErr.Code)
	}
}

func TestAddItem_KeepsDifferentVariantsAsSeparateLines(t *testing.T) {
	uc, catalog := newTestCartUseCase()
	catalog.products["product-1"] = &adapter.ProductInfo{ID: "product-1", Name: "Shirt", PriceAmount: 100000, Currency: "VND", IsVisible: true, HasVariants: true}
	catalog.variants["variant-s"] = &adapter.VariantInfo{ID: "variant-s", ProductID: "product-1", SKU: "SHIRT-S", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "S"}}}
	catalog.variants["variant-m"] = &adapter.VariantInfo{ID: "variant-m", ProductID: "product-1", SKU: "SHIRT-M", Options: []adapter.VariantOptionInfo{{AttributeName: "Size", OptionValue: "M"}}}
	ctx := t.Context()

	variantS, variantM := "variant-s", "variant-m"
	if err := uc.AddItem(ctx, "user-1", "product-1", &variantS, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.AddItem(ctx, "user-1", "product-1", &variantM, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Adding variant-s again must accumulate onto the same line, not create a third.
	if err := uc.AddItem(ctx, "user-1", "product-1", &variantS, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines, err := uc.View(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected two separate lines (one per variant), got %+v", lines)
	}
	for _, l := range lines {
		if l.VariantID == nil {
			t.Fatalf("expected every line to carry a variant id, got %+v", l)
		}
		if *l.VariantID == "variant-s" && l.Quantity != 5 {
			t.Errorf("expected variant-s's quantity to accumulate to 5, got %d", l.Quantity)
		}
		if *l.VariantID == "variant-m" && l.Quantity != 1 {
			t.Errorf("expected variant-m's quantity to stay 1, got %d", l.Quantity)
		}
		if l.VariantLabel == nil || *l.VariantLabel == "" {
			t.Errorf("expected a resolved variant label, got %+v", l.VariantLabel)
		}
	}
}
