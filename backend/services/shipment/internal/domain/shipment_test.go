package domain_test

import (
	"testing"

	"shopee/backend/services/shipment/internal/domain"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
		want bool
	}{
		{domain.StatusPending, domain.StatusReadyToShip, true},
		{domain.StatusPending, domain.StatusCancelled, true},
		{domain.StatusReadyToShip, domain.StatusShipped, true},
		{domain.StatusReadyToShip, domain.StatusCancelled, true},
		{domain.StatusShipped, domain.StatusDelivered, true},
		{domain.StatusShipped, domain.StatusCancelled, false},               // already handed to the carrier — can't cancel directly
		{domain.StatusShipped, domain.StatusInterceptionRequested, true},    // the only way to pull it back
		{domain.StatusInterceptionRequested, domain.StatusCancelled, true},  // carrier accepted
		{domain.StatusInterceptionRequested, domain.StatusShipped, true},    // carrier rejected, delivery continues
		{domain.StatusInterceptionRequested, domain.StatusDelivered, false}, // must resolve through shipped first
		{domain.StatusPending, domain.StatusShipped, false},                 // can't skip ready_to_ship
		{domain.StatusPending, domain.StatusDelivered, false},               // can't skip straight to delivered
		{domain.StatusDelivered, domain.StatusShipped, false},               // no going backwards
		{domain.StatusDelivered, domain.StatusDelivered, false},             // delivered is terminal
		{domain.StatusCancelled, domain.StatusPending, false},               // cancelled is terminal
	}

	for _, tt := range tests {
		if got := domain.CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestIsCancellable(t *testing.T) {
	if !domain.IsCancellable(domain.StatusPending) {
		t.Error("expected pending to be cancellable")
	}
	if !domain.IsCancellable(domain.StatusReadyToShip) {
		t.Error("expected ready_to_ship to be cancellable")
	}
	if domain.IsCancellable(domain.StatusShipped) {
		t.Error("expected shipped to not be cancellable")
	}
	if domain.IsCancellable(domain.StatusDelivered) {
		t.Error("expected delivered to not be cancellable")
	}
	if domain.IsCancellable(domain.StatusCancelled) {
		t.Error("expected cancelled to not be cancellable again")
	}
	if domain.IsCancellable(domain.StatusInterceptionRequested) {
		t.Error("expected interception_requested to not be directly cancellable — it resolves via the carrier's decision")
	}
}

func TestValidateTrackingNumber(t *testing.T) {
	if err := domain.ValidateTrackingNumber(""); err == nil {
		t.Error("expected an error for a missing tracking number")
	}
	if err := domain.ValidateTrackingNumber("TRACK123"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReturnedAndFinalStatuses(t *testing.T) {
	if !domain.CanTransition(domain.StatusShipped, domain.StatusReturned) || !domain.CanTransition(domain.StatusInterceptionRequested, domain.StatusReturned) {
		t.Fatal("a package in transit can come back")
	}
	for _, s := range []domain.Status{domain.StatusDelivered, domain.StatusCancelled, domain.StatusReturned} {
		if !s.Final() || domain.CanTransition(s, domain.StatusShipped) {
			t.Fatalf("%s must be final", s)
		}
	}
	if domain.CanTransition(domain.StatusPending, domain.StatusReturned) {
		t.Fatal("an unshipped package cannot be returned")
	}
}

func TestTrackingNumberAndQuoteInput(t *testing.T) {
	if got, err := domain.NormalizeTrackingNumber("  GHN-123.45 "); err != nil || got != "GHN-123.45" {
		t.Fatalf("unexpected %q %v", got, err)
	}
	for _, bad := range []string{"", "  ", "ab", "has space", "<script>"} {
		if _, err := domain.NormalizeTrackingNumber(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if domain.ValidateQuoteInput("v", "HN", 0) == nil {
		t.Fatal("a missing weight cannot be priced")
	}
	if domain.ValidateQuoteInput("v", "HN", 2_000_000) == nil {
		t.Fatal("an absurd weight is refused")
	}
}

func TestVendorViewDropsContactDetailsOnceFinal(t *testing.T) {
	phone, street, province := "0900000000", "1 Test St", "HN"
	s := &domain.Shipment{Status: domain.StatusShipped, Phone: &phone, StreetAddress: &street, Province: &province}
	if s.ForVendor().Phone == nil {
		t.Fatal("the vendor needs the address while shipping")
	}
	s.Status = domain.StatusDelivered
	v := s.ForVendor()
	if v.Phone != nil || v.StreetAddress != nil || v.Province == nil || s.Phone == nil {
		t.Fatal("a final shipment shows the vendor the region only, without changing the record")
	}
}
