package usecase

import (
	"context"
	"time"

	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// Transactor runs writes of several repositories in one transaction.
type Transactor interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

type ShipmentRepositoryPort interface {
	Create(ctx context.Context, s *domain.Shipment) error
	CreateReplacement(ctx context.Context, s *domain.Shipment, operationID string) error
	FindByReplacementOperation(ctx context.Context, operationID string) (*domain.Shipment, error)
	FindByID(ctx context.Context, id string) (*domain.Shipment, error)
	LockByID(ctx context.Context, id string) (*domain.Shipment, error)
	FindByVendorOrderID(ctx context.Context, vendorOrderID string) (*domain.Shipment, error)
	ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.Shipment, error)
	ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.Shipment, error)
	Transition(ctx context.Context, s *domain.Shipment, to domain.Status, c repository.Change) error
	UpdateTracking(ctx context.Context, s *domain.Shipment, tracking string) error
	RecordFailedAttempt(ctx context.Context, s *domain.Shipment, reason string) error
	FindByInterceptProviderRef(ctx context.Context, providerRef string) (*domain.Shipment, error)
	ListAttention(ctx context.Context, kind string, limit, offset int) ([]*domain.Shipment, error)
	AttentionCounts(ctx context.Context) (map[string]int64, error)
	RedactAddresses(ctx context.Context, retention time.Duration, limit int) (int64, error)
}

type TrackingEventRepositoryPort interface {
	Insert(ctx context.Context, e *domain.TrackingEvent) (bool, error)
	Exists(ctx context.Context, shipmentID, key string) (bool, error)
	ListForShipment(ctx context.Context, shipmentID string) ([]*domain.TrackingEvent, error)
}

// OutboxPort tells Order about shipped/delivered/returned, durably.
type OutboxPort interface {
	Enqueue(ctx context.Context, e repository.OutboxEvent) error
	EnqueueLegacyReturned(ctx context.Context, limit int) (int64, error)
	Requeue(ctx context.Context, id string) error
	Problems(ctx context.Context, limit int) ([]repository.OutboxProblem, error)
	Counts(ctx context.Context) (pending, review int64, err error)
}

// CarrierSimulator stands in for a carrier's dispatcher reporting an
// interception decision; only the mock carrier supplies one.
type CarrierSimulator interface {
	BuildSignedEvent(providerReferenceID string, accepted bool, reason string) (payload []byte, signature string, err error)
}

// VendorGateway checks vendor approval without owning vendor data.
type VendorGateway interface {
	GetApprovedVendorID(ctx context.Context, userID, vendorID, permission string) (string, error)
}

// OrderGateway reads a vendor order's owner, destination and whether Order
// lets it ship. Order remains the owner of order status.
type OrderGateway interface {
	GetVendorOrder(ctx context.Context, vendorOrderID string) (*adapter.VendorOrderSnapshot, error)
	// ClaimHandover asks Order for the right to hand the package to the
	// carrier (AF-03 fence). Order refuses while a cancellation is open, so
	// a cancel and a handover never both win.
	ClaimHandover(ctx context.Context, vendorOrderID, shipmentID string) error
}

// RoleVerifier re-checks an admin's role with Identity.
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// AuditPort appends an admin action in the caller's transaction.
type AuditPort interface {
	Record(ctx context.Context, a domain.AdminAction) error
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
