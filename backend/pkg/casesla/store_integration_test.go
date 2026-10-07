package casesla

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

//go:embed schema.sql
var schemaSQL string

func testStore(t *testing.T) Store {
	t.Helper()
	raw := os.Getenv("CASE_SLA_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("CASE_SLA_TEST_DATABASE_URL not configured")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil {
		t.Fatal("invalid test database config")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("requires a _test database")
	}
	admin, e := pgxpool.NewWithConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	schema := "sla_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(t.Context(), "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, e := pgxpool.NewWithConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if _, e = pool.Exec(t.Context(), schemaSQL); e != nil {
		t.Fatal(e)
	}
	return Store{Pool: pool}
}

type allowRole struct{}

func (allowRole) RequireRole(context.Context, string, string) error { return nil }

type rejectRole struct{}

func (rejectRole) RequireRole(context.Context, string, string) error {
	return apperror.Forbidden("locked")
}

type recordedPublisher struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (p *recordedPublisher) Publish(_ context.Context, _ eventbus.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail {
		return errors.New("test broker offline")
	}
	return nil
}
func createDeadline(t *testing.T, s Store, at time.Time) *Item {
	t.Helper()
	var i *Item
	e := pgx.BeginFunc(t.Context(), s.Pool, func(tx pgx.Tx) error {
		var e error
		i, e = Sync(t.Context(), tx, StageInput{ResourceType: "support", ResourceID: uuid.NewString(), Stage: "acknowledgement", WaitingOn: "admin", Duration: 24 * time.Hour, At: at, CreatedAt: at, URL: "/admin/support/test"})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func TestTwoWorkersRestartAndOutboxRecovery(t *testing.T) {
	s := testStore(t)
	i := createDeadline(t, s, time.Now().UTC().Add(-25*time.Hour))
	pub := &recordedPublisher{fail: true}
	recipient := uuid.NewString()
	w := Worker{Store: s, Owner: "order", Roles: allowRole{}, Publisher: pub, Config: config.CaseSLA{Enabled: true, PollInterval: time.Minute, OnCallIDs: []string{recipient}, EscalationIDs: []string{recipient}}, Log: zerolog.Nop()}
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Go(func() {
			if e := w.Tick(t.Context()); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	var receipts, outbox int
	if e := s.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM case_sla_reminder_receipts),(SELECT count(*) FROM case_sla_notice_outbox)`).Scan(&receipts, &outbox); e != nil {
		t.Fatal(e)
	}
	if receipts != 1 || outbox != 1 {
		t.Fatalf("duplicate overdue: %d %d", receipts, outbox)
	}
	pub.mu.Lock()
	pub.fail = false
	pub.mu.Unlock()
	if _, e := s.Pool.Exec(t.Context(), `UPDATE case_sla_notice_outbox SET next_attempt_at=now(); UPDATE case_sla_work_items SET next_check_at=now()`); e != nil {
		t.Fatal(e)
	}
	if e := w.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	var delivered bool
	if e := s.Pool.QueryRow(t.Context(), `SELECT delivered_at IS NOT NULL FROM case_sla_notice_outbox`).Scan(&delivered); e != nil || !delivered {
		t.Fatal("outbox failed to recover", e)
	}
	page, e := s.List(t.Context(), Filter{Status: "overdue", Limit: 20}, time.Now())
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != i.ID {
		t.Fatal("queue missing overdue", e)
	}
}
func TestMutationReplayVersionAuditAndRollback(t *testing.T) {
	s := testStore(t)
	i := createDeadline(t, s, time.Now().UTC().Add(-25*time.Hour))
	actor := uuid.NewString()
	m := Mutation{ExpectedVersion: i.Version, Reason: "Need carrier confirmation", NewDueAt: time.Now().UTC().Add(time.Hour)}
	out, e := s.Mutate(t.Context(), i.ID, actor, "extensions", "extension-test", m, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if out.BreachedAt == nil {
		t.Fatal("retroactive extension erased breach")
	}
	replay, e := s.Mutate(t.Context(), i.ID, actor, "extensions", "extension-test", m, time.Now())
	if e != nil || replay.Version != out.Version {
		t.Fatal("lost response replay", e)
	}
	m.Reason = "different"
	if _, e = s.Mutate(t.Context(), i.ID, actor, "extensions", "extension-test", m, time.Now()); e == nil {
		t.Fatal("reused key accepted")
	}
	if _, e = s.Mutate(t.Context(), i.ID, actor, "assignments", "another-command", m, time.Now()); e == nil {
		t.Fatal("stale command accepted")
	}
	var count int
	if e = s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM case_sla_audit`).Scan(&count); e != nil || count != 1 {
		t.Fatal("wrong audit count", count, e)
	}
	if _, e = s.Pool.Exec(t.Context(), `DELETE FROM case_sla_audit`); e == nil {
		t.Fatal("audit mutable")
	}
	// An audit failure must roll back the deadline too.
	if _, e = s.Pool.Exec(t.Context(), `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'test audit failure'; END;$$; CREATE TRIGGER fail_audit BEFORE INSERT ON case_sla_audit FOR EACH ROW EXECUTE FUNCTION fail_audit()`); e != nil {
		t.Fatal(e)
	}
	m.ExpectedVersion = out.Version
	m.AssigneeID = actor
	if _, e = s.Mutate(t.Context(), i.ID, actor, "assignments", "rollback-test", m, time.Now()); e == nil {
		t.Fatal("audit failure ignored")
	}
	page, e := s.List(t.Context(), Filter{Limit: 20}, time.Now())
	if e != nil || page.Items[0].AssigneeID != nil {
		t.Fatal("partial commit", e)
	}
}
func TestShadowLegacyAndRevokedAssignee(t *testing.T) {
	s := testStore(t)
	i := createDeadline(t, s, time.Now().UTC().Add(-5*24*time.Hour))
	actor := uuid.NewString()
	_, e := s.Mutate(t.Context(), i.ID, actor, "assignments", "assign-test", Mutation{ExpectedVersion: 1, Reason: "On call", AssigneeID: actor}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	w := Worker{Store: s, Owner: "order", Roles: rejectRole{}, Config: config.CaseSLA{}, Log: zerolog.Nop()}
	if e = w.Tick(t.Context()); e != nil {
		t.Fatal(e)
	}
	p, e := s.List(t.Context(), Filter{Limit: 20}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if p.Items[0].AssigneeID != nil || !p.Items[0].NeedsAttention {
		t.Fatal("revoked assignment or attention lost")
	}
	var notices int
	if e = s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM case_sla_reminder_receipts`).Scan(&notices); e != nil || notices != 0 {
		t.Fatal("shadow consumed receipts", e)
	}
}
