package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// fakeCarrier answers by street; during runs before the answer (an admin
// deciding while the carrier is asked).
type fakeCarrier struct {
	calls  int
	during func()
}

func (c *fakeCarrier) CheckAddress(_ context.Context, a domain.VendorAddress) (bool, string, string, error) {
	c.calls++
	if c.during != nil {
		c.during()
		c.during = nil
	}
	switch a.StreetAddress {
	case "Unserved street":
		return false, "Outside the carrier's area", "ref-2", nil
	case "Manual street":
		return false, "", "", usecase.ErrAddressCheckUnsupported
	case "Flaky street":
		return false, "", "", errors.New("shipment unreachable")
	}
	return true, "", "ref-1", nil
}

// PW-042: each new version of a return destination is checked once by the
// carrier through Shipment. Deliverable verifies it, undeliverable rejects
// it with the carrier's reason, no address check leaves it to an admin, a
// failed call is retried, and an admin's decision made meanwhile stands.
func TestCarrierChecksEachReturnDestinationVersion(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	v := f.shop(t)
	destinations := repository.ReturnDestinationRepository{Pool: f.db}
	f.addresses.Destinations = destinations
	carrier := &fakeCarrier{}
	uc := &usecase.ReturnDestinationUseCase{Destinations: destinations, Addresses: repository.NewVendorAddressRepository(f.db), Vendors: f.vendors,
		Audit: f.audit, Ops: f.ops, Carrier: carrier, Log: zerolog.Nop()}
	addresses, err := f.addresses.ListMine(ctx, f.owner, v.ID, 10, 0)
	if err != nil || len(addresses) != 1 {
		t.Fatalf("addresses: %v %v", addresses, err)
	}
	address := addresses[0]
	moveTo := func(street string) {
		t.Helper()
		if _, err := f.addresses.Update(ctx, f.owner, v.ID, address.ID, "Test owner", "0900000000", "Test province", "Test district", "Test ward", street); err != nil {
			t.Fatal(err)
		}
	}
	check := func() *domain.ReturnDestination {
		t.Helper()
		if _, err := uc.CheckWithCarrier(ctx, 10); err != nil {
			t.Fatal(err)
		}
		d, err := uc.AdminGet(ctx, f.admin, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	count := func(sql string) int {
		t.Helper()
		var n int
		if err := f.db.QueryRow(ctx, sql, v.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if _, err := uc.Set(ctx, f.owner, v.ID, address.ID, "8:00-17:00"); err != nil {
		t.Fatal(err)
	}
	d := check()
	if !d.Verified() || !d.VerifiedByCarrier() || d.CurrentCarrierCheck().Result != domain.CarrierDeliverable {
		t.Fatalf("the carrier verifies a deliverable address: %+v", d)
	}
	if _, err := uc.Verified(ctx, v.ID); err != nil {
		t.Fatal("Order reads a destination the carrier verified")
	}
	if check(); carrier.calls != 1 {
		t.Fatal("a version is checked once")
	}

	moveTo("Unserved street")
	d = check()
	if d.Verified() || d.RejectionReason == nil || *d.RejectionReason != "Carrier address check: Outside the carrier's area" {
		t.Fatalf("an undeliverable address is rejected with the carrier's reason: %+v", d)
	}

	moveTo("Manual street")
	d = check()
	if d.Verified() || d.RejectionReason != nil || d.CurrentCarrierCheck().Result != domain.CarrierUnsupported {
		t.Fatalf("no address check leaves it to an admin: %+v", d)
	}
	if d, err = uc.Decide(ctx, f.admin, v.ID, d.Version, true, "Đã gọi kho"); err != nil || d.VerifiedByCarrier() {
		t.Fatalf("an admin verifies: %+v %v", d, err)
	}

	moveTo("Flaky street")
	if _, err := uc.CheckWithCarrier(ctx, 10); err == nil {
		t.Fatal("a failed call is reported")
	}
	if d, _ = uc.AdminGet(ctx, f.admin, v.ID); d.CurrentCarrierCheck() != nil {
		t.Fatal("a failed call records nothing and is retried")
	}

	// The carrier refuses while an admin verifies: the admin's decision stands.
	moveTo("Unserved street")
	version := d.Version + 1
	carrier.during = func() {
		if _, err := uc.Decide(ctx, f.admin, v.ID, version, true, "Kho đã xác nhận qua điện thoại"); err != nil {
			t.Error(err)
		}
	}
	d = check()
	if !d.Verified() || d.VerifiedByCarrier() || d.CurrentCarrierCheck().Result != domain.CarrierUndeliverable {
		t.Fatalf("the admin's decision stands, the carrier's answer is kept for reference: %+v", d)
	}

	if n := count(`SELECT count(*) FROM vendor_audit_logs WHERE vendor_id = $1 AND action = 'return_destination_carrier_verified'`); n != 1 {
		t.Fatalf("carrier verifications audited: %d", n)
	}
	if n := count(`SELECT count(*) FROM vendor_audit_logs WHERE vendor_id = $1 AND action = 'return_destination_carrier_rejected'`); n != 1 {
		t.Fatalf("carrier rejections audited: %d", n)
	}
	if n := count(`SELECT count(*) FROM vendor_notification_outbox WHERE vendor_id = $1 AND type = 'return_destination_rejected'`); n != 1 {
		t.Fatalf("the owner hears the carrier's rejection: %d", n)
	}
}
