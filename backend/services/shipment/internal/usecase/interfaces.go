package usecase

import (
	"context"
	"time"

	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/domain"
)

type ShipmentRepositoryPort interface {
	Create(ctx context.Context, s *domain.Shipment) error
	FindByID(ctx context.Context, id string) (*domain.Shipment, error)
	FindByVendorOrderID(ctx context.Context, vendorOrderID string) (*domain.Shipment, error)
	ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Shipment, error)
	ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error)
	Advance(ctx context.Context, id string, status domain.Status, trackingNumber *string, shippedAt, deliveredAt *time.Time) error
	RequestInterception(ctx context.Context, id, providerRef string) (applied bool, err error)
	ResolveInterception(ctx context.Context, id string, newStatus domain.Status) (shipment *domain.Shipment, applied bool, err error)
	FindByInterceptProviderRef(ctx context.Context, providerRef string) (*domain.Shipment, error)
}

// CarrierSimulator stands in for a real carrier's dispatcher reporting an
// interception decision, mirroring Payment's Simulator — only present when
// this deployment is wired with the mock carrier adapter.
type CarrierSimulator interface {
	BuildSignedEvent(providerReferenceID string, accepted bool, reason string) (payload []byte, signature string, err error)
}

// VendorGateway lets the use case check vendor approval without owning any
// vendor data itself.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID string) (string, error)
}

// OrderGateway lets the use case verify a vendor sub-order's owner and
// payment status without owning any order data itself. Order remains the
// sole owner of order/vendor-order lifecycle status.
type OrderGateway interface {
	GetVendorOrder(ctx context.Context, vendorOrderID string) (*adapter.VendorOrderSnapshot, error)
}

type CarrierRepositoryPort interface {
	Create(ctx context.Context, c *domain.Carrier) error
	FindByID(ctx context.Context, id string) (*domain.Carrier, error)
	List(ctx context.Context) ([]*domain.Carrier, error)
	SetActive(ctx context.Context, id string, isActive bool) error
}

type ZoneRepositoryPort interface {
	Create(ctx context.Context, z *domain.Zone) error
	FindByID(ctx context.Context, id string) (*domain.Zone, error)
	List(ctx context.Context) ([]*domain.Zone, error)
	AddProvince(ctx context.Context, zoneID, provinceCode string) error
	ListProvinces(ctx context.Context, zoneID string) ([]string, error)
	FindZoneByProvinceCode(ctx context.Context, provinceCode string) (*domain.Zone, error)
}

type FeeRuleRepositoryPort interface {
	CurrentVersion(ctx context.Context, carrierID, zoneID string) (int, error)
	Insert(ctx context.Context, f *domain.FeeRule) error
	FindCurrent(ctx context.Context, carrierID, zoneID string) (*domain.FeeRule, error)
	List(ctx context.Context) ([]*domain.FeeRule, error)
}

type VendorShippingMethodRepositoryPort interface {
	Create(ctx context.Context, m *domain.VendorShippingMethod) error
	ListForVendor(ctx context.Context, vendorID string) ([]*domain.VendorShippingMethod, error)
	FindByID(ctx context.Context, id string) (*domain.VendorShippingMethod, error)
	FindDefaultForVendor(ctx context.Context, vendorID string) (*domain.VendorShippingMethod, error)
	SetDefault(ctx context.Context, vendorID, methodID string) error
	SetActive(ctx context.Context, vendorID, methodID string, isActive bool) error
}

type TrackingEventRepositoryPort interface {
	Insert(ctx context.Context, e *domain.TrackingEvent) error
	ListForShipment(ctx context.Context, shipmentID string) ([]*domain.TrackingEvent, error)
}
