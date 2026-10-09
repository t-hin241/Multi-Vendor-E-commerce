package repository_test

import (
	"testing"

	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// AF-05: only the owner designates a return destination; Order sees it
// only once an admin verified that exact version; editing the address
// needs a new verification; the destination's address cannot be deleted.
func TestReturnDestinationNeedsVerificationOfTheCurrentVersion(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	v := f.shop(t)
	destinations := repository.ReturnDestinationRepository{Pool: f.db}
	f.addresses.Destinations = destinations
	uc := &usecase.ReturnDestinationUseCase{Destinations: destinations, Addresses: repository.NewVendorAddressRepository(f.db), Vendors: f.vendors,
		Audit: f.audit, Ops: f.ops}

	addresses, err := f.addresses.ListMine(ctx, f.owner, v.ID, 10, 0)
	if err != nil || len(addresses) != 1 {
		t.Fatalf("addresses: %v %v", addresses, err)
	}
	pickup := addresses[0]
	if _, err := uc.Verified(ctx, v.ID); err == nil {
		t.Fatal("a pickup address is not a return destination by default")
	}
	if _, err := uc.Set(ctx, f.other, v.ID, pickup.ID, "8-17h"); err == nil {
		t.Fatal("only the owner designates the destination")
	}
	d, err := uc.Set(ctx, f.owner, v.ID, pickup.ID, "8:00-17:00, thứ 2 đến thứ 6")
	if err != nil || d.Version != 1 || d.Verified() {
		t.Fatalf("designated, not verified: %+v %v", d, err)
	}
	if _, err := uc.Verified(ctx, v.ID); err == nil {
		t.Fatal("Order sees no destination before verification")
	}
	if _, err := uc.Decide(ctx, f.owner, v.ID, 1, true, "ok"); err == nil {
		t.Fatal("only an admin verifies")
	}
	if d, err = uc.Decide(ctx, f.admin, v.ID, 1, true, "Đã gọi xác nhận kho"); err != nil || !d.Verified() {
		t.Fatalf("verified: %+v %v", d, err)
	}
	if got, err := uc.Verified(ctx, v.ID); err != nil || got.Address.StreetAddress != "Test street" {
		t.Fatalf("Order reads the verified destination: %+v %v", got, err)
	}

	if _, err := f.addresses.Update(ctx, f.owner, v.ID, pickup.ID, "Test owner", "0900000000", "Test province", "Test district", "Test ward", "New street"); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Verified(ctx, v.ID); err == nil {
		t.Fatal("an edited address needs a new verification")
	}
	if _, err := uc.Decide(ctx, f.admin, v.ID, 1, true, "old version"); err == nil {
		t.Fatal("verifying an old version is a conflict")
	}
	if d, err = uc.Decide(ctx, f.admin, v.ID, 2, true, "Đã kiểm lại"); err != nil || !d.Verified() || d.Address.StreetAddress != "New street" {
		t.Fatalf("the new version verified: %+v %v", d, err)
	}
	if err := f.addresses.Delete(ctx, f.owner, v.ID, pickup.ID); err == nil {
		t.Fatal("the destination's address cannot be deleted")
	}
	var audits int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vendor_audit_logs WHERE vendor_id = $1 AND action LIKE 'return_destination_%'`, v.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("set + two verifications are audited, got %d", audits)
	}
}
