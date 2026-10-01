package repository_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
	"shopee/backend/services/shipment/internal/usecase"
)

type denyAdmin struct{}

func (denyAdmin) RequireRole(context.Context, string, string) error {
	return apperror.Forbidden("Active account with required role needed")
}

// ADM-01: shipping configuration changes need a re-verified admin and a
// reason where they change what shops can sell, and are audited with the
// change in one transaction.
func TestShippingConfigChangesAreVerifiedAndAudited(t *testing.T) {
	e := newEnv(t, false)
	ctx := middleware.ContextWithRequestID(t.Context(), "req-config-0001")
	admin := uuid.NewString()
	config := usecase.AdminConfig{Tx: repository.Transactions{Pool: e.pool}, Identity: allowAdmin{}, Audit: repository.AuditRepository{Pool: e.pool}}
	carriers := usecase.NewCarrierUseCase(repository.NewCarrierRepository(e.pool), config)
	feeRules := usecase.NewFeeRuleUseCase(repository.NewFeeRuleRepository(e.pool), repository.NewCarrierRepository(e.pool), repository.NewZoneRepository(e.pool), config)

	if err := carriers.SetActive(ctx, admin, e.carrier, false, ""); err == nil {
		t.Fatal("turning a carrier off needs a reason")
	}
	if err := carriers.SetActive(ctx, admin, e.carrier, false, "Carrier stopped service"); err != nil {
		t.Fatal(err)
	}
	if _, err := feeRules.SetCurrent(ctx, e.carrier, e.zone, 25000, 1000, 5000, admin, "New carrier price list"); err != nil {
		t.Fatal(err)
	}
	var changes string
	if err := e.pool.QueryRow(t.Context(), `SELECT changes::text FROM shipment_admin_audit WHERE action='carrier_active_set' AND actor_id=$1 AND request_id='req-config-0001'`, admin).
		Scan(&changes); err != nil || changes != `{"is_active": [true, false]}` {
		t.Fatalf("carrier change audit: %q %v", changes, err)
	}
	if n := e.count(t, `SELECT count(*) FROM shipment_admin_audit WHERE action='fee_rule_set' AND actor_id=$1`, admin); n != 1 {
		t.Fatalf("expected the fee rule audited once, got %d", n)
	}

	// A non-admin is refused by the use case itself, and nothing changes.
	refused := usecase.NewCarrierUseCase(repository.NewCarrierRepository(e.pool), usecase.AdminConfig{Tx: config.Tx, Identity: denyAdmin{}, Audit: config.Audit})
	var app *apperror.Error
	if err := refused.SetActive(ctx, uuid.NewString(), e.carrier, true, "test"); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	// Without an audit store nothing is changed.
	if err := usecase.NewCarrierUseCase(repository.NewCarrierRepository(e.pool), usecase.AdminConfig{Tx: config.Tx, Identity: allowAdmin{}}).
		SetActive(ctx, admin, e.carrier, true, "test"); err == nil {
		t.Fatal("a change without an audit store must fail")
	}
	// A failed audit write rolls the change back.
	if _, err := e.pool.Exec(t.Context(), `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `CREATE TRIGGER fail_audit BEFORE INSERT ON shipment_admin_audit FOR EACH ROW EXECUTE FUNCTION fail_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := carriers.SetActive(ctx, admin, e.carrier, true, "Carrier is back"); err == nil {
		t.Fatal("the change must fail with its audit")
	}
	if n := e.count(t, `SELECT count(*) FROM carriers WHERE id=$1 AND is_active`, e.carrier); n != 0 {
		t.Fatal("the carrier must stay off when its audit could not be written")
	}
	if _, err := e.pool.Exec(t.Context(), `DROP TRIGGER fail_audit ON shipment_admin_audit`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`UPDATE shipment_admin_audit SET reason='x'`, `DELETE FROM shipment_admin_audit`} {
		if _, err := e.pool.Exec(t.Context(), stmt); err == nil {
			t.Fatalf("%s must be refused", stmt)
		}
	}
}

// Admin fulfillment actions are audited; the vendor's own steps stay on
// the shipment timeline only.
func TestAdminFulfillmentActionsAreAudited(t *testing.T) {
	e := newEnv(t, false)
	ctx := t.Context()
	s := e.paidShipment(t)
	vendor := usecase.Actor{ID: e.userA, Role: domain.ActorVendor}
	admin := usecase.Actor{ID: uuid.NewString(), Role: domain.ActorAdmin}
	if _, err := e.uc.MarkShipped(ctx, vendor, s.ID, "TRACK-AUD-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.RecordFailedAttempt(ctx, admin, s.ID, "Nobody home"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.uc.MarkDelivered(ctx, admin, s.ID, "Carrier confirmed by phone"); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM shipment_admin_audit WHERE entity_id=$1`, s.ID); n != 2 {
		t.Fatalf("expected the two admin actions audited, got %d", n)
	}
	var changes string
	if err := e.pool.QueryRow(ctx, `SELECT changes::text FROM shipment_admin_audit WHERE entity_id=$1 AND action='shipment_delivered'`, s.ID).Scan(&changes); err != nil ||
		changes != `{"status": ["shipped", "delivered"]}` {
		t.Fatalf("delivery audit: %q %v", changes, err)
	}

	src := adminaudit.Source{Name: "shipment", SQL: repository.AuditSearchSQL, DB: e.pool, Roles: allowAdmin{}}
	q, _ := url.ParseQuery("entity_type=shipment&entity_id=" + s.ID + "&limit=1")
	f, _ := adminaudit.ParseFilter(q)
	first, err := src.Search(ctx, admin.ID, f)
	if err != nil || len(first) != 1 || first[0].Action != "shipment_delivered" {
		t.Fatalf("newest first: %+v %v", first, err)
	}
	f.CursorTime, f.CursorID = &first[0].OccurredAt, first[0].ID
	second, err := src.Search(ctx, admin.ID, f)
	if err != nil || len(second) != 1 || second[0].Action != "delivery_attempt_failed" {
		t.Fatalf("next page: %+v %v", second, err)
	}
}
