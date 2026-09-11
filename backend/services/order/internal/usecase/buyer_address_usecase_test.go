package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
)

func TestAddAddress_FirstAddressBecomesDefault(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()

	// newCheckoutFixture already seeds one default address for buyer-1, so
	// use a fresh buyer to test the "first address" rule in isolation.
	first, err := f.uc.AddAddress(ctx, "buyer-2", "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.IsDefault {
		t.Error("expected the first address to become the default")
	}

	second, err := f.uc.AddAddress(ctx, "buyer-2", "Nguyen B", "0911111111", "HCM", "D2", "W2", "456 St")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.IsDefault {
		t.Error("expected a subsequent address to not automatically become the default")
	}
}

func TestAddresses_OnlyOneDefaultAtATime(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()

	first, _ := f.uc.AddAddress(ctx, "buyer-2", "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")
	second, _ := f.uc.AddAddress(ctx, "buyer-2", "Nguyen B", "0911111111", "HCM", "D2", "W2", "456 St")

	if err := f.uc.SetDefaultAddress(ctx, "buyer-2", second.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list, err := f.uc.ListMyAddresses(ctx, "buyer-2")
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

func TestBuyerAddress_RejectsAccessFromAnotherBuyer(t *testing.T) {
	f := newCheckoutFixture()
	ctx := t.Context()
	address, _ := f.uc.AddAddress(ctx, "buyer-2", "Nguyen A", "0900000000", "HN", "D1", "W1", "123 St")

	err := f.uc.DeleteAddress(ctx, "buyer-3", address.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}
