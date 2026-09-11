package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// mustAppError is defined once in vendor_usecase_test.go and shared across
// this package's test files.

func newAddressFixture(t *testing.T) (*usecase.VendorAddressUseCase, *fakeVendorRepository, string) {
	t.Helper()
	vendors := newFakeVendorRepository()
	addresses := newFakeVendorAddressRepository()
	uc := usecase.NewVendorAddressUseCase(addresses, vendors)

	v := &domain.Vendor{UserID: "user-a", ShopName: "Shop A"}
	if err := vendors.Create(t.Context(), v); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return uc, vendors, "user-a"
}

func TestAdd_FirstAddressBecomesDefault(t *testing.T) {
	uc, _, userID := newAddressFixture(t)
	ctx := t.Context()

	first, err := uc.Add(ctx, userID, "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.IsDefault {
		t.Error("expected the first address to become the default")
	}

	second, err := uc.Add(ctx, userID, "Nguyen B", "0911111111", "HCM", "D2", "W2", "456 St")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.IsDefault {
		t.Error("expected a subsequent address to not automatically become the default")
	}
}

func TestAddresses_OnlyOneDefaultAtATime(t *testing.T) {
	uc, _, userID := newAddressFixture(t)
	ctx := t.Context()

	first, _ := uc.Add(ctx, userID, "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")
	second, _ := uc.Add(ctx, userID, "Nguyen B", "0911111111", "HCM", "D2", "W2", "456 St")

	if err := uc.SetDefault(ctx, userID, second.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list, err := uc.ListMine(ctx, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defaults := 0
	for _, a := range list {
		if a.IsDefault {
			defaults++
			if a.ID != second.ID {
				t.Errorf("expected %s to be the default, found %s marked default instead", second.ID, a.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly one default address, got %d", defaults)
	}
	_ = first
}

func TestVendorAddress_RejectsAccessFromAnotherVendor(t *testing.T) {
	uc, vendors, userID := newAddressFixture(t)
	ctx := t.Context()
	address, _ := uc.Add(ctx, userID, "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")

	other := &domain.Vendor{UserID: "user-b", ShopName: "Shop B"}
	if err := vendors.Create(ctx, other); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := uc.Delete(ctx, "user-b", address.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}
