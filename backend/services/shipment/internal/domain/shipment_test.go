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
