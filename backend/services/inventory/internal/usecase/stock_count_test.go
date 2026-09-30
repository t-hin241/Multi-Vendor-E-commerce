package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
)

func stockedItem(t *testing.T, f *fixture, available, reserved int64) *domain.InventoryItem {
	t.Helper()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.catalog.productOwners["product-1"] = "vendor-1"
	item, err := f.uc.CreateItem(t.Context(), "user-1", strPtr("product-1"), nil, available)
	if err != nil {
		t.Fatal(err)
	}
	f.items.byProduct["product-1"].ReservedQuantity = reserved
	return item
}

func TestRecordStockCount_WritesDownAvailableAndKeepsReserved(t *testing.T) {
	f := newFixture()
	item := stockedItem(t, f, 10, 3)

	// 12 units on hand: 3 held for orders, so 9 are available (1 lost).
	count, replayed, err := f.uc.RecordStockCount(t.Context(), "user-1", item.ID, "count-1", 12, "  1 unit damaged ")
	if err != nil || replayed {
		t.Fatalf("unexpected %v replayed=%v", err, replayed)
	}
	if count.NewAvailable != 9 || count.PreviousAvailable != 10 || count.ReservedAtCount != 3 || count.Reason != "1 unit damaged" {
		t.Fatalf("unexpected count %+v", count)
	}
	stored := f.items.byProduct["product-1"]
	if stored.AvailableQuantity != 9 || stored.ReservedQuantity != 3 {
		t.Fatalf("reserved must not change: %+v", stored)
	}

	// A retried request is not applied twice.
	again, replayed, err := f.uc.RecordStockCount(t.Context(), "user-1", item.ID, "count-1", 12, "1 unit damaged")
	if err != nil || !replayed || again.NewAvailable != 9 || f.items.byProduct["product-1"].AvailableQuantity != 9 {
		t.Fatalf("expected a replay, got %+v %v %v", again, replayed, err)
	}
	_, _, err = f.uc.RecordStockCount(t.Context(), "user-1", item.ID, "count-1", 11, "1 unit damaged")
	if mustAppError(t, err).Code != apperror.CodeConflict {
		t.Fatalf("reusing a count id with other values must conflict, got %v", err)
	}
}

func TestRecordStockCount_RejectsIncreasesAndCountsBelowReserved(t *testing.T) {
	f := newFixture()
	item := stockedItem(t, f, 10, 3)
	ctx := t.Context()

	cases := []struct {
		name    string
		counted int64
		reason  string
		code    apperror.Code
	}{
		{"adds stock", 14, "found extra", apperror.CodeConflict},
		{"below reserved", 2, "missing", apperror.CodeConflict},
		{"negative", -1, "typo", apperror.CodeValidation},
		{"no reason", 5, "   ", apperror.CodeValidation},
	}
	for _, tc := range cases {
		_, _, err := f.uc.RecordStockCount(ctx, "user-1", item.ID, "count-"+tc.name, tc.counted, tc.reason)
		if mustAppError(t, err).Code != tc.code {
			t.Errorf("%s: expected %s, got %v", tc.name, tc.code, err)
		}
	}
	if got := f.items.byProduct["product-1"]; got.AvailableQuantity != 10 || got.ReservedQuantity != 3 {
		t.Fatalf("rejected counts must not change stock: %+v", got)
	}
}

func TestRecordStockCount_RejectsAnotherVendorsItem(t *testing.T) {
	f := newFixture()
	item := stockedItem(t, f, 10, 0)
	f.vendors.approvedVendors["user-2"] = "vendor-2"

	_, _, err := f.uc.RecordStockCount(t.Context(), "user-2", item.ID, "count-x", 5, "not mine")
	if mustAppError(t, err).Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	_, _, err = f.uc.RecordStockCount(t.Context(), "user-1", "missing-item", "count-y", 5, "gone")
	if mustAppError(t, err).Code != apperror.CodeNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}
