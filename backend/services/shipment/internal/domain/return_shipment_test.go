package domain

import "testing"

// AF-05: only a parcel not received or closed can still move; a carrier
// name is bounded.
func TestReturnShipmentStatus(t *testing.T) {
	if !ReturnPendingDispatch.Active() || !ReturnInTransit.Active() || ReturnReceived.Active() || ReturnDeliveryException.Active() {
		t.Fatal("pending and in-transit parcels are active, the others closed")
	}
	if name, err := ValidateCarrierName("  GHN  "); err != nil || name != "GHN" {
		t.Fatalf("carrier: %q %v", name, err)
	}
	if _, err := ValidateCarrierName(""); err == nil {
		t.Fatal("a carrier is required")
	}
}
