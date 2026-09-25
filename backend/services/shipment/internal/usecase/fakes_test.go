package usecase_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/carrier"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

type fakeShipmentRepository struct {
	mu            sync.Mutex
	byID          map[string]*domain.Shipment
	byVendorOrder map[string]string
	nextID        int
}

func newFakeShipmentRepository() *fakeShipmentRepository {
	return &fakeShipmentRepository{byID: map[string]*domain.Shipment{}, byVendorOrder: map[string]string{}}
}

func (f *fakeShipmentRepository) Create(_ context.Context, s *domain.Shipment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.byVendorOrder[s.VendorOrderID]; exists {
		return repository.ErrShipmentAlreadyExists
	}
	f.nextID++
	s.ID = fmt.Sprintf("shipment-%d", f.nextID)
	cp := *s
	f.byID[s.ID] = &cp
	f.byVendorOrder[s.VendorOrderID] = s.ID
	return nil
}

func (f *fakeShipmentRepository) FindByID(_ context.Context, id string) (*domain.Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrShipmentNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeShipmentRepository) FindByVendorOrderID(_ context.Context, vendorOrderID string) (*domain.Shipment, error) {
	f.mu.Lock()
	id, ok := f.byVendorOrder[vendorOrderID]
	f.mu.Unlock()
	if !ok {
		return nil, repository.ErrShipmentNotFound
	}
	return f.FindByID(context.Background(), id)
}

func (f *fakeShipmentRepository) ListByVendor(_ context.Context, vendorID string, _, _ int) ([]*domain.Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Shipment
	for _, s := range f.byID {
		if s.VendorID == vendorID {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeShipmentRepository) ListByBuyer(_ context.Context, buyerID string, _, _ int) ([]*domain.Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Shipment
	for _, s := range f.byID {
		if s.BuyerID == buyerID {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeShipmentRepository) Advance(_ context.Context, id string, status domain.Status, trackingNumber *string, shippedAt, deliveredAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok {
		return repository.ErrShipmentNotFound
	}
	s.Status = status
	if trackingNumber != nil {
		s.TrackingNumber = trackingNumber
	}
	if shippedAt != nil {
		s.ShippedAt = shippedAt
	}
	if deliveredAt != nil {
		s.DeliveredAt = deliveredAt
	}
	return nil
}

func (f *fakeShipmentRepository) RequestInterception(_ context.Context, id, providerRef string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok || s.Status != domain.StatusShipped {
		return false, nil
	}
	s.Status = domain.StatusInterceptionRequested
	s.InterceptProviderRef = &providerRef
	now := time.Now()
	s.InterceptRequestedAt = &now
	return true, nil
}

func (f *fakeShipmentRepository) ResolveInterception(_ context.Context, id string, newStatus domain.Status) (*domain.Shipment, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok || s.Status != domain.StatusInterceptionRequested || s.InterceptResolvedAt != nil {
		return nil, false, nil
	}
	s.Status = newStatus
	now := time.Now()
	s.InterceptResolvedAt = &now
	cp := *s
	return &cp, true, nil
}

func (f *fakeShipmentRepository) FindByInterceptProviderRef(_ context.Context, providerRef string) (*domain.Shipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.byID {
		if s.InterceptProviderRef != nil && *s.InterceptProviderRef == providerRef {
			cp := *s
			return &cp, nil
		}
	}
	return nil, repository.ErrShipmentNotFound
}

type fakeCarrierProvider struct {
	nextID int
	fail   bool
}

func (f *fakeCarrierProvider) RequestInterception(_ context.Context, _ carrier.RequestInterceptionInput) (carrier.RequestInterceptionResult, error) {
	if f.fail {
		return carrier.RequestInterceptionResult{}, apperror.Internal(fmt.Errorf("carrier unreachable"))
	}
	f.nextID++
	return carrier.RequestInterceptionResult{ProviderReferenceID: fmt.Sprintf("intercept-%d", f.nextID)}, nil
}

// fakeCarrierVerifierSimulator is both carrier.Verifier and
// usecase.CarrierSimulator: signing and verifying are the identity
// function here, since these tests only need the payload to round-trip,
// not real HMAC signing (that's covered by carrier/mock's own tests).
type fakeCarrierVerifierSimulator struct{}

func (fakeCarrierVerifierSimulator) BuildSignedEvent(providerReferenceID string, accepted bool, reason string) ([]byte, string, error) {
	payload := []byte(fmt.Sprintf("%s|%t|%s", providerReferenceID, accepted, reason))
	return payload, "sig", nil
}

func (fakeCarrierVerifierSimulator) Verify(payload []byte, signatureHeader string) (carrier.DecisionEvent, error) {
	if signatureHeader != "sig" {
		return carrier.DecisionEvent{}, fmt.Errorf("invalid signature")
	}
	parts := splitPipe(string(payload))
	if len(parts) != 3 {
		return carrier.DecisionEvent{}, fmt.Errorf("malformed payload")
	}
	return carrier.DecisionEvent{ProviderReferenceID: parts[0], Accepted: parts[1] == "true", Reason: parts[2]}, nil
}

func splitPipe(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

type fakeVendorGateway struct {
	approvedVendors map[string]string
}

func newFakeVendorGateway() *fakeVendorGateway {
	return &fakeVendorGateway{approvedVendors: map[string]string{}}
}

func (f *fakeVendorGateway) GetApprovedVendorID(_ context.Context, userID, vendorID string) (string, error) {
	approved, ok := f.approvedVendors[userID]
	if !ok || approved != vendorID {
		return "", apperror.Forbidden("You must have an approved vendor account")
	}
	return vendorID, nil
}

type fakeOrderGateway struct {
	vendorOrders map[string]*adapter.VendorOrderSnapshot
}

func newFakeOrderGateway() *fakeOrderGateway {
	return &fakeOrderGateway{vendorOrders: map[string]*adapter.VendorOrderSnapshot{}}
}

func (f *fakeOrderGateway) GetVendorOrder(_ context.Context, vendorOrderID string) (*adapter.VendorOrderSnapshot, error) {
	vo, ok := f.vendorOrders[vendorOrderID]
	if !ok {
		return nil, apperror.NotFound("Order not found")
	}
	cp := *vo
	return &cp, nil
}

type fakeVendorShippingMethodRepository struct {
	byVendor map[string][]*domain.VendorShippingMethod
	nextID   int
}

func newFakeVendorShippingMethodRepository() *fakeVendorShippingMethodRepository {
	return &fakeVendorShippingMethodRepository{byVendor: map[string][]*domain.VendorShippingMethod{}}
}

func (f *fakeVendorShippingMethodRepository) Create(_ context.Context, m *domain.VendorShippingMethod) error {
	f.nextID++
	m.ID = fmt.Sprintf("method-%d", f.nextID)
	f.byVendor[m.VendorID] = append(f.byVendor[m.VendorID], m)
	return nil
}

func (f *fakeVendorShippingMethodRepository) ListForVendor(_ context.Context, vendorID string) ([]*domain.VendorShippingMethod, error) {
	return f.byVendor[vendorID], nil
}

func (f *fakeVendorShippingMethodRepository) FindByID(_ context.Context, id string) (*domain.VendorShippingMethod, error) {
	for _, methods := range f.byVendor {
		for _, m := range methods {
			if m.ID == id {
				return m, nil
			}
		}
	}
	return nil, repository.ErrVendorShippingMethodNotFound
}

func (f *fakeVendorShippingMethodRepository) FindDefaultForVendor(_ context.Context, vendorID string) (*domain.VendorShippingMethod, error) {
	for _, m := range f.byVendor[vendorID] {
		if m.IsDefault && m.IsActive {
			return m, nil
		}
	}
	return nil, repository.ErrVendorShippingMethodNotFound
}

func (f *fakeVendorShippingMethodRepository) SetDefault(_ context.Context, vendorID, methodID string) error {
	found := false
	for _, m := range f.byVendor[vendorID] {
		if m.ID == methodID {
			found = true
		}
	}
	if !found {
		return repository.ErrVendorShippingMethodNotFound
	}
	for _, m := range f.byVendor[vendorID] {
		m.IsDefault = m.ID == methodID
	}
	return nil
}

func (f *fakeVendorShippingMethodRepository) SetActive(_ context.Context, vendorID, methodID string, isActive bool) error {
	for _, m := range f.byVendor[vendorID] {
		if m.ID == methodID {
			m.IsActive = isActive
			return nil
		}
	}
	return repository.ErrVendorShippingMethodNotFound
}

type fakeZoneRepository struct {
	byID       map[string]*domain.Zone
	byProvince map[string]string // province code -> zone id
	nextID     int
}

func newFakeZoneRepository() *fakeZoneRepository {
	return &fakeZoneRepository{byID: map[string]*domain.Zone{}, byProvince: map[string]string{}}
}

func (f *fakeZoneRepository) Create(_ context.Context, z *domain.Zone) error {
	f.nextID++
	z.ID = fmt.Sprintf("zone-%d", f.nextID)
	f.byID[z.ID] = z
	return nil
}

func (f *fakeZoneRepository) FindByID(_ context.Context, id string) (*domain.Zone, error) {
	z, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrZoneNotFound
	}
	return z, nil
}

func (f *fakeZoneRepository) List(_ context.Context) ([]*domain.Zone, error) {
	var out []*domain.Zone
	for _, z := range f.byID {
		out = append(out, z)
	}
	return out, nil
}

func (f *fakeZoneRepository) AddProvince(_ context.Context, zoneID, provinceCode string) error {
	if _, exists := f.byProvince[provinceCode]; exists {
		return repository.ErrProvinceAlreadyMapped
	}
	f.byProvince[provinceCode] = zoneID
	return nil
}

func (f *fakeZoneRepository) ListProvinces(_ context.Context, zoneID string) ([]string, error) {
	var out []string
	for code, zid := range f.byProvince {
		if zid == zoneID {
			out = append(out, code)
		}
	}
	return out, nil
}

func (f *fakeZoneRepository) FindZoneByProvinceCode(_ context.Context, provinceCode string) (*domain.Zone, error) {
	zoneID, ok := f.byProvince[provinceCode]
	if !ok {
		return nil, repository.ErrZoneNotFound
	}
	return f.byID[zoneID], nil
}

type feeRuleKey struct{ carrierID, zoneID string }

type fakeFeeRuleRepository struct {
	current map[feeRuleKey]*domain.FeeRule
	nextID  int
}

func newFakeFeeRuleRepository() *fakeFeeRuleRepository {
	return &fakeFeeRuleRepository{current: map[feeRuleKey]*domain.FeeRule{}}
}

func (f *fakeFeeRuleRepository) CurrentVersion(_ context.Context, carrierID, zoneID string) (int, error) {
	if r, ok := f.current[feeRuleKey{carrierID, zoneID}]; ok {
		return r.Version, nil
	}
	return 0, nil
}

func (f *fakeFeeRuleRepository) Insert(_ context.Context, r *domain.FeeRule) error {
	f.nextID++
	r.ID = fmt.Sprintf("fee-rule-%d", f.nextID)
	f.current[feeRuleKey{r.CarrierID, r.ZoneID}] = r
	return nil
}

func (f *fakeFeeRuleRepository) FindCurrent(_ context.Context, carrierID, zoneID string) (*domain.FeeRule, error) {
	r, ok := f.current[feeRuleKey{carrierID, zoneID}]
	if !ok {
		return nil, repository.ErrFeeRuleNotFound
	}
	return r, nil
}

func (f *fakeFeeRuleRepository) List(_ context.Context) ([]*domain.FeeRule, error) {
	var out []*domain.FeeRule
	for _, r := range f.current {
		out = append(out, r)
	}
	return out, nil
}

type fakeTrackingEventRepository struct {
	byShipment map[string][]*domain.TrackingEvent
	nextID     int
}

func newFakeTrackingEventRepository() *fakeTrackingEventRepository {
	return &fakeTrackingEventRepository{byShipment: map[string][]*domain.TrackingEvent{}}
}

func (f *fakeTrackingEventRepository) Insert(_ context.Context, e *domain.TrackingEvent) error {
	f.nextID++
	e.ID = fmt.Sprintf("event-%d", f.nextID)
	f.byShipment[e.ShipmentID] = append(f.byShipment[e.ShipmentID], e)
	return nil
}

func (f *fakeTrackingEventRepository) ListForShipment(_ context.Context, shipmentID string) ([]*domain.TrackingEvent, error) {
	return f.byShipment[shipmentID], nil
}
