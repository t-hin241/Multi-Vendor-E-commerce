package usecase_test

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

type fixture struct {
	uc            *usecase.ShipmentUseCase
	shipments     *fakeShipmentRepository
	vendorMethods *fakeVendorShippingMethodRepository
	zones         *fakeZoneRepository
	feeRules      *fakeFeeRuleRepository
	events        *fakeTrackingEventRepository
	vendors       *fakeVendorGateway
	orders        *fakeOrderGateway
	carrier       *fakeCarrierProvider
	carrierCodec  fakeCarrierVerifierSimulator
}

func newFixture() *fixture {
	shipments := newFakeShipmentRepository()
	vendorMethods := newFakeVendorShippingMethodRepository()
	zones := newFakeZoneRepository()
	feeRules := newFakeFeeRuleRepository()
	events := newFakeTrackingEventRepository()
	vendors := newFakeVendorGateway()
	orders := newFakeOrderGateway()
	carrierProvider := &fakeCarrierProvider{}
	carrierCodec := fakeCarrierVerifierSimulator{}
	uc := usecase.NewShipmentUseCase(
		shipments, vendorMethods, zones, feeRules, events, vendors, orders,
		carrierProvider, carrierCodec, carrierCodec, zerolog.Nop(),
	)
	return &fixture{
		uc: uc, shipments: shipments, vendorMethods: vendorMethods, zones: zones,
		feeRules: feeRules, events: events, vendors: vendors, orders: orders,
		carrier: carrierProvider, carrierCodec: carrierCodec,
	}
}

// setUpShippingConfig gives a vendor a default carrier and a fee rule for
// the given province's zone — the baseline every "happy path" test needs,
// since CreateAuto now requires all three to be configured.
func (f *fixture) setUpShippingConfig(t *testing.T, vendorID, province string) {
	t.Helper()
	method := &domain.VendorShippingMethod{VendorID: vendorID, CarrierID: "carrier-1", IsDefault: true, IsActive: true}
	if err := f.vendorMethods.Create(t.Context(), method); err != nil {
		t.Fatalf("setup: %v", err)
	}
	zone := &domain.Zone{Name: "Zone 1", Code: "z1"}
	if err := f.zones.Create(t.Context(), zone); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := f.zones.AddProvince(t.Context(), zone.ID, province); err != nil {
		t.Fatalf("setup: %v", err)
	}
	rule := &domain.FeeRule{CarrierID: "carrier-1", ZoneID: zone.ID, Version: 1, BaseFeeAmount: 15000, FreeWeightGrams: 500, ExtraFeePerKg: 5000}
	if err := f.feeRules.Insert(t.Context(), rule); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func baseInput(vendorOrderID, vendorID, buyerID, province string) usecase.CreateShipmentInput {
	return usecase.CreateShipmentInput{
		VendorOrderID: vendorOrderID, VendorID: vendorID, BuyerID: buyerID, PackageWeightGrams: 1000,
		RecipientName: "Nguyen Van A", Phone: "0900000000", Province: province,
		District: "District 1", Ward: "Ward 1", StreetAddress: "123 Main St",
	}
}

func TestCreateAuto_UsesVendorDefaultCarrierAndComputesFee(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")

	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shipment.Status != domain.StatusPending {
		t.Errorf("expected a new shipment to start pending, got %q", shipment.Status)
	}
	if shipment.CarrierID == nil || *shipment.CarrierID != "carrier-1" {
		t.Errorf("expected the vendor's default carrier to be used, got %v", shipment.CarrierID)
	}
	if shipment.FeeAmount != 20000 { // 15000 base + 1 extra kg over the 500g allowance
		t.Errorf("expected fee 20000, got %d", shipment.FeeAmount)
	}
	if shipment.BuyerID != "buyer-1" {
		t.Errorf("expected buyer_id to be denormalized onto the shipment, got %q", shipment.BuyerID)
	}
}

func TestCreateAuto_IsIdempotent(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")

	first, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected a retried create to return the same shipment, got %s and %s", first.ID, second.ID)
	}
}

func TestCreateAuto_FailsWhenVendorHasNoDefaultShippingMethod(t *testing.T) {
	f := newFixture()
	ctx := t.Context()

	_, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for missing shipping method, got %v", appErr.Code)
	}
}

func TestCreateAuto_FailsWhenDestinationHasNoZone(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")

	_, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "UNKNOWN_PROVINCE"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an unmapped province, got %v", appErr.Code)
	}
}

func TestCreateAuto_FailsWhenNoFeeRuleConfiguredForTheZone(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	method := &domain.VendorShippingMethod{VendorID: "vendor-a", CarrierID: "carrier-1", IsDefault: true, IsActive: true}
	if err := f.vendorMethods.Create(ctx, method); err != nil {
		t.Fatalf("setup: %v", err)
	}
	zone := &domain.Zone{Name: "Zone 1", Code: "z1"}
	if err := f.zones.Create(ctx, zone); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := f.zones.AddProvince(ctx, zone.ID, "HN"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// deliberately no fee rule inserted

	_, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a missing fee rule, got %v", appErr.Code)
	}
}

func TestCreateOrGet_RecreatesFromOrderSnapshotWhenAutoCreateFailedEarlier(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.orders.vendorOrders["vo-1"] = &adapter.VendorOrderSnapshot{
		ID: "vo-1", VendorID: "vendor-a", Status: "pending_payment",
		BuyerID: "buyer-1", RecipientName: "Nguyen Van A", Phone: "0900000000",
		Province: "HN", District: "District 1", Ward: "Ward 1", StreetAddress: "123 Main St",
		PackageWeightGrams: 1000,
	}

	shipment, err := f.uc.CreateOrGet(ctx, "user-a", "vo-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shipment.FeeAmount != 20000 {
		t.Errorf("expected the fallback path to compute the same fee as CreateAuto, got %d", shipment.FeeAmount)
	}
}

func TestCreateOrGet_RejectsCancelledOrRefundedOrder(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.orders.vendorOrders["vo-1"] = &adapter.VendorOrderSnapshot{ID: "vo-1", VendorID: "vendor-a", Status: "cancelled"}

	_, err := f.uc.CreateOrGet(ctx, "user-a", "vo-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for a cancelled order, got %v", appErr.Code)
	}
}

func TestCreateOrGet_RejectsNonOwningVendor(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.orders.vendorOrders["vo-1"] = &adapter.VendorOrderSnapshot{ID: "vo-1", VendorID: "vendor-b", Status: "paid"}

	_, err := f.uc.CreateOrGet(ctx, "user-a", "vo-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestCancelForVendorOrder_VoidsAPendingShipment(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := f.shipments.FindByID(ctx, shipment.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestCancelForVendorOrder_IsANoOpWhenNoShipmentExists(t *testing.T) {
	f := newFixture()
	if err := f.uc.CancelForVendorOrder(t.Context(), "vo-does-not-exist"); err != nil {
		t.Errorf("expected no error when there's nothing to cancel, got %v", err)
	}
}

// shipItOut advances a freshly created shipment straight to "shipped", the
// state that triggers carrier interception on cancel instead of a direct
// cancel.
func (f *fixture) shipItOut(t *testing.T, userID, shipmentID string) {
	t.Helper()
	if _, err := f.uc.Advance(t.Context(), userID, shipmentID, domain.StatusReadyToShip, ""); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := f.uc.Advance(t.Context(), userID, shipmentID, domain.StatusShipped, "TRACK123"); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestCancelForVendorOrder_RequestsInterceptionWhenAlreadyShipped(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)

	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := f.shipments.FindByID(ctx, shipment.ID)
	if got.Status != domain.StatusInterceptionRequested {
		t.Errorf("expected interception_requested, got %q", got.Status)
	}
	if got.InterceptProviderRef == nil {
		t.Error("expected a provider reference id to be recorded")
	}
}

func TestCancelForVendorOrder_NoopWhenInterceptionAlreadyRequested(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)
	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	before, _ := f.shipments.FindByID(ctx, shipment.ID)

	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after, _ := f.shipments.FindByID(ctx, shipment.ID)
	if *after.InterceptProviderRef != *before.InterceptProviderRef {
		t.Error("expected a second cancel request not to request a new interception")
	}
}

func TestProcessCarrierWebhook_AcceptedCancelsShipment(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)
	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requested, _ := f.shipments.FindByID(ctx, shipment.ID)

	payload, signature, err := f.carrierCodec.BuildSignedEvent(*requested.InterceptProviderRef, true, "picked back up at the hub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := f.shipments.FindByID(ctx, shipment.ID)
	if got.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
	if got.InterceptResolvedAt == nil {
		t.Error("expected intercept_resolved_at to be stamped")
	}
}

func TestProcessCarrierWebhook_RejectedRevertsToShipped(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)
	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requested, _ := f.shipments.FindByID(ctx, shipment.ID)

	payload, signature, err := f.carrierCodec.BuildSignedEvent(*requested.InterceptProviderRef, false, "already out for delivery")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := f.shipments.FindByID(ctx, shipment.ID)
	if got.Status != domain.StatusShipped {
		t.Errorf("expected the shipment to revert to shipped, got %q", got.Status)
	}

	// Delivery can still proceed normally afterward.
	if _, err := f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusDelivered, ""); err != nil {
		t.Errorf("expected delivery to still be possible after a rejected interception, got %v", err)
	}
}

func TestProcessCarrierWebhook_DuplicateDeliveryIsIdempotent(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)
	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requested, _ := f.shipments.FindByID(ctx, shipment.ID)
	payload, signature, err := f.carrierCodec.BuildSignedEvent(*requested.InterceptProviderRef, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := f.uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A second, duplicate delivery of the exact same event must not error
	// and must not re-apply anything.
	if err := f.uc.ProcessCarrierWebhook(ctx, payload, signature); err != nil {
		t.Fatalf("expected a duplicate delivery to be a silent no-op, got %v", err)
	}

	got, _ := f.shipments.FindByID(ctx, shipment.ID)
	if got.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestProcessCarrierWebhook_RejectsInvalidSignature(t *testing.T) {
	f := newFixture()
	if err := f.uc.ProcessCarrierWebhook(t.Context(), []byte("payload"), "not-the-real-signature"); err == nil {
		t.Error("expected an error for an invalid signature")
	}
}

func TestSimulateCarrierDecision_RequiresInterceptionRequestedStatus(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Still "pending" — no interception request in flight yet.
	_, err = f.uc.SimulateCarrierDecision(ctx, "user-a", shipment.ID, true, "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict when no interception request is pending, got %v", appErr.Code)
	}
}

func TestSimulateCarrierDecision_AppliesTheDecision(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f.shipItOut(t, "user-a", shipment.ID)
	if err := f.uc.CancelForVendorOrder(ctx, "vo-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := f.uc.SimulateCarrierDecision(ctx, "user-a", shipment.ID, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Status != domain.StatusCancelled {
		t.Errorf("expected cancelled, got %q", updated.Status)
	}
}

func TestAdvance_RequiresTrackingNumberToShip(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusReadyToShip, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusShipped, "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for missing tracking number, got %v", appErr.Code)
	}

	shipped, err := f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusShipped, "TRACK123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shipped.Status != domain.StatusShipped {
		t.Errorf("expected shipped, got %q", shipped.Status)
	}
	if shipped.ShippedAt == nil {
		t.Error("expected shipped_at to be stamped")
	}
}

func TestAdvance_RecordsATrackingEventOnEveryTransition(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusReadyToShip, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events, err := f.uc.ListEventsForShipment(ctx, "user-a", shipment.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// one for the initial "pending" at creation, one for the transition to
	// ready_to_ship.
	if len(events) != 2 {
		t.Fatalf("expected 2 tracking events, got %d", len(events))
	}
	if events[0].Status != domain.StatusPending || events[1].Status != domain.StatusReadyToShip {
		t.Errorf("unexpected event sequence: %v", events)
	}
}

func TestAdvance_RejectsSkippingAStep(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.Advance(ctx, "user-a", shipment.ID, domain.StatusShipped, "TRACK123")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict skipping ready_to_ship, got %v", appErr.Code)
	}
}

func TestAdvance_RejectsNonOwningVendor(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	f.vendors.approvedVendors["user-a"] = "vendor-a"
	f.vendors.approvedVendors["user-b"] = "vendor-b"
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.Advance(ctx, "user-b", shipment.ID, domain.StatusReadyToShip, "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestListEventsForShipment_AllowsTheOwningBuyer(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events, err := f.uc.ListEventsForShipment(ctx, "buyer-1", shipment.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 tracking event, got %d", len(events))
	}
}

func TestListEventsForShipment_RejectsAnUnrelatedUser(t *testing.T) {
	f := newFixture()
	ctx := t.Context()
	f.setUpShippingConfig(t, "vendor-a", "HN")
	shipment, err := f.uc.CreateAuto(ctx, baseInput("vo-1", "vendor-a", "buyer-1", "HN"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = f.uc.ListEventsForShipment(ctx, "someone-else", shipment.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}
