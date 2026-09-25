package domain

import "time"

// VendorShippingMethod is a vendor enabling one of the admin's carriers for
// their own shop. Exactly one enabled method per vendor may be the default
// (enforced by a partial unique index) — the one the system automatically
// uses to quote and create a shipment, since the buyer never picks a
// carrier in this design.
type VendorShippingMethod struct {
	ID        string
	VendorID  string
	CarrierID string
	IsDefault bool
	IsActive  bool
	CreatedAt time.Time
}
