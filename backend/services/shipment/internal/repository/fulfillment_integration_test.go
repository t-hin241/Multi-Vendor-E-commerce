package repository_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/adapter"
	"shopee/backend/services/shipment/internal/carrier/manual"
	"shopee/backend/services/shipment/internal/carrier/mock"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

func shipmentDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("SHIPMENT_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("SHIPMENT_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("database name must end in _test")
	}
	ctx := t.Context()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("open test database")
	}
	schema := "shipment_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("open isolated schema")
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	files, _ := filepath.Glob("../../migrations/*.up.sql")
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var applyErr error
		for attempt := 0; attempt < 5; attempt++ {
			if _, applyErr = pool.Exec(ctx, string(sql)); applyErr == nil || !strings.Contains(applyErr.Error(), "pg_extension_name_index") {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
		}
		if applyErr != nil {
			t.Fatalf("migration %s: %v", filepath.Base(f), applyErr)
		}
	}
	return pool
}

type fakeVendors struct{ owners map[string]string } // user -> vendor

func (f fakeVendors) GetApprovedVendorID(_ context.Context, userID, vendorID, _ string) (string, error) {
	if f.owners[userID] != vendorID {
		return "", apperror.Forbidden("not your shop")
	}
	return vendorID, nil
}

type fakeOrders struct {
	mu  sync.Mutex
	vos map[string]*adapter.VendorOrderSnapshot
}

func (f *fakeOrders) GetVendorOrder(_ context.Context, id string) (*adapter.VendorOrderSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vo, ok := f.vos[id]
	if !ok {
		return nil, apperror.NotFound("Order not found")
	}
	cp := *vo
	return &cp, nil
}

type allowAdmin struct{}

func (allowAdmin) RequireRole(context.Context, string, string) error { return nil }

type env struct {
	pool    *pgxpool.Pool
	uc      *usecase.ShipmentUseCase
	orders  *fakeOrders
	vendors fakeVendors
	mock    *mock.Provider
	vendorA string
	userA   string
	carrier string
	zone    string
}

func newEnv(t *testing.T, withMock bool) *env {
	pool := shipmentDB(t)
	ctx := t.Context()
	e := &env{pool: pool, orders: &fakeOrders{vos: map[string]*adapter.VendorOrderSnapshot{}}, vendorA: uuid.NewString(), userA: uuid.NewString()}
	e.vendors = fakeVendors{owners: map[string]string{e.userA: e.vendorA}}
	if err := pool.QueryRow(ctx, `INSERT INTO carriers (name, code) VALUES ('Test carrier', 'TST') RETURNING id`).Scan(&e.carrier); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO shipping_zones (name, code) VALUES ('North', 'N') RETURNING id`).Scan(&e.zone); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO shipping_zone_provinces (zone_id, province_code) VALUES ($1, 'HN')`, []any{e.zone}},
		{`INSERT INTO shipping_fee_rules (carrier_id, zone_id, version, base_fee_amount, free_weight_grams, extra_fee_per_kg) VALUES ($1, $2, 1, 20000, 1000, 5000)`, []any{e.carrier, e.zone}},
		{`INSERT INTO vendor_shipping_methods (vendor_id, carrier_id, is_default) VALUES ($1, $2, true)`, []any{e.vendorA, e.carrier}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	deps := usecase.Deps{Tx: repository.Transactions{Pool: pool}, Shipments: repository.NewShipmentRepository(pool),
		VendorMethods: repository.NewVendorShippingMethodRepository(pool), Carriers: repository.NewCarrierRepository(pool),
		Zones: repository.NewZoneRepository(pool), FeeRules: repository.NewFeeRuleRepository(pool), Events: repository.NewTrackingEventRepository(pool), Outbox: repository.OrderOutbox{Pool: pool},
		Vendors: e.vendors, Orders: e.orders, Identity: allowAdmin{}, Audit: repository.AuditRepository{Pool: pool}, Carrier: manual.Provider{}, Verifier: manual.Provider{}, Log: zerolog.Nop()}
	if withMock {
		e.mock = mock.New("fake-carrier-secret-not-a-real-secret")
		deps.Carrier, deps.Verifier, deps.Simulator = e.mock, e.mock, e.mock
	}
	e.uc = usecase.NewShipmentUseCase(deps)
	return e
}

// paidShipment opens the shipment of a vendor order Order released.
func (e *env) paidShipment(t *testing.T) *domain.Shipment {
	t.Helper()
	vo := uuid.NewString()
	e.orders.mu.Lock()
	e.orders.vos[vo] = &adapter.VendorOrderSnapshot{ID: vo, VendorID: e.vendorA, Status: "paid", Fulfillable: true, BuyerID: uuid.NewString()}
	e.orders.mu.Unlock()
	s, err := e.uc.CreateAuto(t.Context(), usecase.CreateShipmentInput{VendorOrderID: vo, VendorID: e.vendorA, BuyerID: uuid.NewString(),
		PackageWeightGrams: 1500, RecipientName: "Test Buyer", Phone: "0900000000", Province: "HN", District: "D", Ward: "W", StreetAddress: "1 Test St"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func expectCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestQuoteIsExplicitAboutPriceExpiryAndUnavailability(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	q, err := e.uc.Quote(ctx, e.vendorA, "HN", 2500)
	if err != nil || q.FeeAmount != 20000+2*5000 || q.Currency != "VND" || q.FeeRuleVersion != 1 || !q.ExpiresAt.After(q.QuotedAt) {
		t.Fatalf("unexpected quote %+v %v", q, err)
	}
	for _, tc := range []struct {
		vendor, province string
		weight           int64
	}{{uuid.NewString(), "HN", 500}, {e.vendorA, "XX", 500}, {e.vendorA, "HN", 0}} {
		_, err := e.uc.Quote(ctx, tc.vendor, tc.province, tc.weight)
		expectCode(t, err, apperror.CodeValidation)
	}
	if e.count(t, `SELECT count(*) FROM shipments`) != 0 {
		t.Fatal("a quote creates nothing")
	}
}

// An admin turning a carrier off stops new quotes on it, so checkout
// shows shipping unavailable; orders the buyer already paid still ship.
func TestInactiveCarrierTakesNoNewQuotes(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	carriers := repository.NewCarrierRepository(e.pool)
	if err := carriers.SetActive(ctx, e.carrier, false); err != nil {
		t.Fatal(err)
	}
	_, err := e.uc.Quote(ctx, e.vendorA, "HN", 2500)
	expectCode(t, err, apperror.CodeValidation)

	var rule string
	if err := e.pool.QueryRow(ctx, `SELECT id FROM shipping_fee_rules LIMIT 1`).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	paid, err := e.uc.CreateAuto(ctx, usecase.CreateShipmentInput{VendorOrderID: uuid.NewString(), VendorID: e.vendorA,
		BuyerID: uuid.NewString(), PackageWeightGrams: 500, RecipientName: "R", Phone: "0900000000", Province: "HN", StreetAddress: "S",
		Quote: &domain.QuotedFee{FeeAmount: 12345, CarrierID: e.carrier, ZoneID: e.zone, FeeRuleID: rule}})
	if err != nil || paid.FeeAmount != 12345 || *paid.CarrierID != e.carrier {
		t.Fatalf("paid order with its checkout fee: %+v %v", paid, err)
	}
	// Paid before checkout snapshotted the fee: priced now, on its carrier.
	if legacy := e.paidShipment(t); legacy.FeeAmount != 20000+5000 || *legacy.CarrierID != e.carrier {
		t.Fatalf("paid order without a snapshot: %+v", legacy)
	}

	if err := carriers.SetActive(ctx, e.carrier, true); err != nil {
		t.Fatal(err)
	}
	if q, err := e.uc.Quote(ctx, e.vendorA, "HN", 2500); err != nil || q.FeeAmount != 20000+2*5000 || q.CarrierID != e.carrier {
		t.Fatalf("quote after the carrier is back: %+v %v", q, err)
	}
}

func TestCreateIsIdempotentAndKeepsTheQuotedFee(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	vo := uuid.NewString()
	in := usecase.CreateShipmentInput{VendorOrderID: vo, VendorID: e.vendorA, BuyerID: uuid.NewString(), PackageWeightGrams: 500,
		RecipientName: "R", Phone: "0900000000", Province: "HN", StreetAddress: "S",
		Quote: &domain.QuotedFee{FeeAmount: 12345, CarrierID: e.carrier, ZoneID: e.zone}}
	var rule string
	_ = e.pool.QueryRow(ctx, `SELECT id FROM shipping_fee_rules LIMIT 1`).Scan(&rule)
	in.Quote.FeeRuleID = rule
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.uc.CreateAuto(context.Background(), in); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if e.count(t, `SELECT count(*) FROM shipments WHERE vendor_order_id = $1 AND fee_amount = 12345`, vo) != 1 {
		t.Fatal("one shipment with the fee the buyer paid")
	}
}

func TestShippingNeedsOrdersReleaseAndTheOwningVendor(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	s := e.paidShipment(t)
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}

	other := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorVendor}
	_, err := e.uc.MarkShipped(ctx, other, s.ID, "TRACK-001")
	expectCode(t, err, apperror.CodeForbidden)

	e.orders.vos[s.VendorOrderID].Fulfillable = false
	_, err = e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-001")
	expectCode(t, err, apperror.CodeConflict)
	e.orders.vos[s.VendorOrderID].Fulfillable = true

	_, err = e.uc.MarkShipped(ctx, vendor, s.ID, " ")
	expectCode(t, err, apperror.CodeValidation)
	shipped, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-001")
	if err != nil || shipped.Status != domain.StatusShipped || *shipped.TrackingNumber != "TRACK-001" {
		t.Fatalf("unexpected %+v %v", shipped, err)
	}
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-001"); err != nil {
		t.Fatal("a retried ship request is accepted")
	}
	if e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'shipped'`, s.ID) != 1 ||
		e.count(t, `SELECT count(*) FROM shipment_tracking_events WHERE shipment_id = $1 AND status = 'shipped' AND actor_role = 'vendor' AND actor_id = $2`, s.ID, e.userA) != 1 {
		t.Fatal("shipping must notify Order once and be audited with its actor")
	}
	_, err = e.uc.Advance(ctx, e.userA, s.ID, domain.StatusCancelled, "")
	expectCode(t, err, apperror.CodeConflict)
}

func TestConcurrentDeliveryAppliesOnceAndNeverGoesBack(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	s := e.paidShipment(t)
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-002"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.uc.MarkDelivered(context.Background(), vendor, s.ID, "Carrier confirmed")
		}()
	}
	wg.Wait()
	if e.count(t, `SELECT count(*) FROM shipment_tracking_events WHERE shipment_id = $1 AND status = 'delivered'`, s.ID) != 1 ||
		e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'delivered'`, s.ID) != 1 {
		t.Fatal("delivery must be recorded and sent once")
	}
	_, err := e.uc.MarkReturned(ctx, vendor, s.ID, "late return claim")
	expectCode(t, err, apperror.CodeConflict)
	_, err = e.uc.RecordFailedAttempt(ctx, vendor, s.ID, "nobody home")
	expectCode(t, err, apperror.CodeConflict)
	stored, _ := repository.NewShipmentRepository(e.pool).FindByID(ctx, s.ID)
	if stored.Status != domain.StatusDelivered {
		t.Fatal("delivered is final")
	}
	if view, _ := e.uc.GetByVendorOrderID(ctx, e.userA, s.VendorOrderID); view.Phone != nil || view.StreetAddress != nil || view.Province == nil {
		t.Fatal("a vendor no longer sees the buyer's contact details once delivered")
	}
}

func TestFailedAttemptsAndReturn(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	s := e.paidShipment(t)
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}
	if _, err := e.uc.MarkShipped(ctx, usecase.Actor{ID: e.userA, Role: domain.ActorVendor}, s.ID, "TRACK-003"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.uc.RecordFailedAttempt(ctx, admin, s.ID, "buyer unreachable"); err != nil {
			t.Fatal(err)
		}
	}
	returned, err := e.uc.MarkReturned(ctx, admin, s.ID, "returned after two attempts")
	if err != nil || returned.Status != domain.StatusReturned || returned.FailedAttempts != 2 {
		t.Fatalf("unexpected %+v %v", returned, err)
	}
	if e.count(t, `SELECT count(*) FROM shipment_outbox WHERE shipment_id = $1 AND event_type = 'returned'`, s.ID) != 1 {
		t.Fatal("Order must learn the package came back")
	}
}

func TestCancellationBeforeAndAfterHandover(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	pending := e.paidShipment(t)
	if err := e.uc.CancelForVendorOrder(ctx, pending.VendorOrderID); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'cancelled' AND cancelled_at IS NOT NULL`, pending.ID) != 1 {
		t.Fatal("an unshipped package is cancelled")
	}
	shipped := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, usecase.Actor{ID: e.userA, Role: domain.ActorVendor}, shipped.ID, "TRACK-004"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.uc.CancelForVendorOrder(ctx, shipped.VendorOrderID); err != nil {
			t.Fatal(err)
		}
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'interception_requested'`, shipped.ID) != 1 {
		t.Fatal("a shipped package waits for the carrier, never assumed stopped")
	}
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}
	resolved, err := e.uc.ResolveInterception(ctx, admin, shipped.ID, false, "Carrier hotline: already out for delivery")
	if err != nil || resolved.Status != domain.StatusShipped {
		t.Fatalf("a refused interception keeps the package in transit: %+v %v", resolved, err)
	}
}

func TestCarrierWebhookIsDeduplicatedAndLateDecisionsIgnored(t *testing.T) {
	e := newEnv(t, true)
	ctx := t.Context()
	s := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, usecase.Actor{ID: e.userA, Role: domain.ActorVendor}, s.ID, "TRACK-005"); err != nil {
		t.Fatal(err)
	}
	if err := e.uc.CancelForVendorOrder(ctx, s.VendorOrderID); err != nil {
		t.Fatal(err)
	}
	stored, _ := repository.NewShipmentRepository(e.pool).FindByID(ctx, s.ID)
	payload, sig, err := e.mock.BuildSignedEvent(*stored.InterceptProviderRef, true, "stopped at hub")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := e.uc.ProcessCarrierWebhook(ctx, payload, sig); err != nil {
			t.Fatal(err)
		}
	}
	if e.count(t, `SELECT count(*) FROM shipment_tracking_events WHERE shipment_id = $1 AND actor_role = 'carrier'`, s.ID) != 1 {
		t.Fatal("a repeated carrier delivery must apply once")
	}
	late, lsig, _ := e.mock.BuildSignedEvent(*stored.InterceptProviderRef, false, "late")
	if err := e.uc.ProcessCarrierWebhook(ctx, late, lsig); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND status = 'cancelled'`, s.ID) != 1 {
		t.Fatal("a late decision must not move the shipment back")
	}
	if err := e.uc.ProcessCarrierWebhook(ctx, payload, "bad"); err == nil {
		t.Fatal("an unsigned delivery must be refused")
	}
}

func TestOutboxDeliversInOrderAndParksRefusals(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	s := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-006"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.MarkDelivered(ctx, vendor, s.ID, ""); err != nil {
		t.Fatal(err)
	}
	outbox := repository.OrderOutbox{Pool: e.pool}
	var seen []domain.OrderEvent
	// Order down for the shipped event: delivered must wait behind it.
	if err := outbox.Dispatch(ctx, func(context.Context, repository.OutboxEvent) error { return errors.New("test order down") }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE shipment_outbox SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	for {
		err := outbox.Dispatch(ctx, func(_ context.Context, ev repository.OutboxEvent) error { seen = append(seen, ev.Type); return nil })
		if errors.Is(err, repository.ErrNothingToDeliver) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != domain.OrderEventShipped || seen[1] != domain.OrderEventDelivered {
		t.Fatalf("expected shipped then delivered, got %v", seen)
	}
	refused := e.paidShipment(t)
	if _, err := e.uc.MarkShipped(ctx, vendor, refused.ID, "TRACK-007"); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Dispatch(ctx, func(context.Context, repository.OutboxEvent) error { return apperror.Conflict("order cancelled") }); err != nil {
		t.Fatal(err)
	}
	problems, _ := outbox.Problems(ctx, 10)
	if len(problems) != 1 || !problems[0].RequiresReview {
		t.Fatalf("Order's refusal must wait for review, got %+v", problems)
	}
	if err := e.uc.RetryOrderEvent(ctx, uuid.NewString(), problems[0].ID, refused.ID, "order reinstated"); err != nil {
		t.Fatal(err)
	}
	if p, _ := outbox.Problems(ctx, 10); len(p) != 0 {
		t.Fatal("a retried event is due again")
	}
}

func TestAddressRetentionAndOperationsView(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	s := e.paidShipment(t)
	stuck := e.paidShipment(t)
	if err := e.uc.CancelForVendorOrder(ctx, s.VendorOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE shipments SET updated_at = now() - interval '200 days' WHERE id = $1`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE shipments SET created_at = now() - interval '5 days' WHERE id = $1`, stuck.ID); err != nil {
		t.Fatal(err)
	}
	n, err := e.uc.RedactAddresses(ctx, 180*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("expected one redaction, got %d %v", n, err)
	}
	if e.count(t, `SELECT count(*) FROM shipments WHERE id = $1 AND phone IS NULL AND street_address IS NULL AND province IS NOT NULL`, s.ID) != 1 {
		t.Fatal("contact details must be removed, region kept")
	}
	ops, err := e.uc.AdminOperations(ctx, uuid.NewString())
	if err != nil || ops.Counts["fulfillment_lag"] != 1 || len(ops.Lists["fulfillment_lag"]) != 1 {
		t.Fatalf("a paid package not shipped for days must show up: %+v %v", ops, err)
	}
}
