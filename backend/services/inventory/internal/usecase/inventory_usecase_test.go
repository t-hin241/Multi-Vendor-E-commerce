package usecase_test

import (
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/usecase"
)

type fixture struct {
	uc      *usecase.InventoryUseCase
	items   *fakeItemRepository
	vendors *fakeVendorGateway
	catalog *fakeCatalogGateway
}

func newFixture() *fixture {
	items := newFakeItemRepository()
	reservations := newFakeReservationRepository(items)
	vendors := newFakeVendorGateway()
	catalog := newFakeCatalogGateway()

	uc := usecase.NewInventoryUseCase(items, reservations, vendors, catalog)
	return &fixture{uc: uc, items: items, vendors: vendors, catalog: catalog}
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func strPtr(s string) *string { return &s }

func TestCreateItem_RejectsNonOwner(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-2" // a different vendor owns it

	_, err := f.uc.CreateItem(t.Context(), "user-1", strPtr("product-1"), nil, 100)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestCreateItem_SucceedsForOwner(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"

	item, err := f.uc.CreateItem(t.Context(), "user-1", strPtr("product-1"), nil, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.AvailableQuantity != 100 {
		t.Errorf("expected available quantity 100, got %d", item.AvailableQuantity)
	}
}

func TestCreateItem_RejectsDuplicateSetup(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-1"), nil, 100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-1"), nil, 50)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict, got %v", appErr.Code)
	}
}

func TestCreateItem_ResolvesOwnershipThroughVariant(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.variantOwners["variant-1"] = fakeVariantOwner{VendorID: "vendor-1", ProductID: "product-1"}
	ctx := t.Context()

	// A client-supplied product_id must be ignored for a variant-scoped
	// call — only the variant's resolved owner/product matter.
	item, err := f.uc.CreateItem(ctx, "user-1", strPtr("someone-elses-product"), strPtr("variant-1"), 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ProductID != "product-1" {
		t.Errorf("expected the resolved product id from the variant lookup, got %q", item.ProductID)
	}
	if item.VariantID == nil || *item.VariantID != "variant-1" {
		t.Errorf("expected variant id to be stored, got %v", item.VariantID)
	}
}

func TestCreateItem_RejectsVariantOwnedByAnotherVendor(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.variantOwners["variant-1"] = fakeVariantOwner{VendorID: "vendor-2", ProductID: "product-1"}

	_, err := f.uc.CreateItem(t.Context(), "user-1", nil, strPtr("variant-1"), 20)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestRestock_WorksForVariant(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.variantOwners["variant-1"] = fakeVariantOwner{VendorID: "vendor-1", ProductID: "product-1"}
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", nil, strPtr("variant-1"), 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, err := f.uc.Restock(ctx, "user-1", nil, strPtr("variant-1"), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.AvailableQuantity != 8 {
		t.Errorf("expected available quantity 8 after restocking, got %d", item.AvailableQuantity)
	}
}

func TestReserve_WorksForVariant(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.variantOwners["variant-1"] = fakeVariantOwner{VendorID: "vendor-1", ProductID: "product-1"}
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", nil, strPtr("variant-1"), 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reservations, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{
		{ProductID: "product-1", VariantID: strPtr("variant-1"), Quantity: 4},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reservations) != 1 || reservations[0].VariantID == nil || *reservations[0].VariantID != "variant-1" {
		t.Fatalf("expected one reservation carrying the variant id, got %+v", reservations)
	}

	item, err := f.items.FindByVariantID(ctx, "variant-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.AvailableQuantity != 6 {
		t.Errorf("expected available quantity 6 after reserving 4 of 10, got %d", item.AvailableQuantity)
	}
	if item.ReservedQuantity != 4 {
		t.Errorf("expected reserved quantity 4, got %d", item.ReservedQuantity)
	}
}

// TestReserve_DoesNotFallBackToPlainProductRowForAVariantProduct guards the
// bug this pass fixed: a product with only variant-scoped stock rows (no
// bare product-level row) must not have its variant's stock line satisfied
// by/confused with a product-level lookup — reserving without a variant id
// for such a product must fail as "not stocked", not silently match
// something else.
func TestReserve_DoesNotFallBackToPlainProductRowForAVariantProduct(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.variantOwners["variant-1"] = fakeVariantOwner{VendorID: "vendor-1", ProductID: "product-1"}
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", nil, strPtr("variant-1"), 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No product-level row exists for product-1 — only a variant-scoped one.
	_, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{{ProductID: "product-1", Quantity: 1}})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict (not stocked at the product level), got %v", appErr.Code)
	}
}

func TestReserve_NeverOversells(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-1"), nil, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Requesting more than available must fail cleanly, not partially reserve.
	_, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{{ProductID: "product-1", Quantity: 10}})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for insufficient stock, got %v", appErr.Code)
	}

	item, err := f.items.FindByProductID(ctx, "product-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.AvailableQuantity != 5 {
		t.Errorf("a failed reservation must not touch available stock, got %d", item.AvailableQuantity)
	}
}

func TestReserve_AllOrNothingAcrossMultipleLines(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"
	f.catalog.productOwners["product-2"] = "vendor-1"
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-1"), nil, 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-2"), nil, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// product-1 has enough stock, product-2 does not: the whole checkout
	// must fail, and product-1's stock must be untouched.
	_, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{
		{ProductID: "product-1", Quantity: 5},
		{ProductID: "product-2", Quantity: 5},
	})
	if err == nil {
		t.Fatal("expected the reservation to fail when any line is short on stock")
	}

	item1, _ := f.items.FindByProductID(ctx, "product-1")
	if item1.AvailableQuantity != 10 {
		t.Errorf("product-1 must be untouched when product-2 fails, got available=%d", item1.AvailableQuantity)
	}
}

func TestReserve_SucceedsAndDecrementsAvailable(t *testing.T) {
	f := newFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"
	ctx := t.Context()

	if _, err := f.uc.CreateItem(ctx, "user-1", strPtr("product-1"), nil, 10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reservations, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{{ProductID: "product-1", Quantity: 4}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reservations) != 1 {
		t.Fatalf("expected one reservation, got %d", len(reservations))
	}

	item, _ := f.items.FindByProductID(ctx, "product-1")
	if item.AvailableQuantity != 6 {
		t.Errorf("expected available quantity 6 after reserving 4 of 10, got %d", item.AvailableQuantity)
	}
	if item.ReservedQuantity != 4 {
		t.Errorf("expected reserved quantity 4, got %d", item.ReservedQuantity)
	}
}

func TestReserve_RejectsMissingProduct(t *testing.T) {
	f := newFixture()

	_, err := f.uc.Reserve(t.Context(), "order-1", []domain.ReservationLine{{ProductID: "no-such-product", Quantity: 1}})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for an unstocked product, got %v", appErr.Code)
	}
}

func TestReserve_ValidatesInput(t *testing.T) {
	f := newFixture()
	ctx := t.Context()

	if _, err := f.uc.Reserve(ctx, "", []domain.ReservationLine{{ProductID: "p1", Quantity: 1}}); err == nil {
		t.Error("expected an error for a missing order id")
	}
	if _, err := f.uc.Reserve(ctx, "order-1", nil); err == nil {
		t.Error("expected an error for an empty line list")
	}
	if _, err := f.uc.Reserve(ctx, "order-1", []domain.ReservationLine{{ProductID: "p1", Quantity: 0}}); err == nil {
		t.Error("expected an error for a zero quantity line")
	}
}

func TestRelease_IsIdempotent(t *testing.T) {
	f := newFixture()
	ctx := t.Context()

	if err := f.uc.Release(ctx, "order-never-reserved"); err != nil {
		t.Errorf("releasing an order with no reservations must succeed, got: %v", err)
	}
}
