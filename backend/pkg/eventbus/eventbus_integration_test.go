package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
)

// world is a real broker and an isolated inbox schema with a table the
// handlers write to.
type world struct {
	bus   *Bus
	inbox Inbox
	pool  *pgxpool.Pool
	typ   string
}

func letters(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rand.IntN(26))
	}
	return string(b)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	natsURL := os.Getenv("EVENTBUS_TEST_NATS_URL")
	if natsURL == "" {
		t.Skip("EVENTBUS_TEST_NATS_URL is not configured")
	}
	return newWorldOn(t, natsURL, Credentials{Service: "eventbustest"})
}

// newWorldOn connects to natsURL as cred.
func newWorldOn(t *testing.T, natsURL string, cred Credentials) *world {
	t.Helper()
	dbURL := os.Getenv("EVENTBUS_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("EVENTBUS_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("test database name must end in _test")
	}
	ctx := t.Context()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	schema := "eventbus_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	if _, err := pool.Exec(ctx, InboxSQL+`CREATE TABLE applied (event_id TEXT PRIMARY KEY, value TEXT);`); err != nil {
		t.Fatal(err)
	}
	bus, err := Connect(natsURL, cred, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	retryDelay = func(int) time.Duration { return 50 * time.Millisecond }
	t.Cleanup(func() { retryDelay = Backoff })
	return &world{bus: bus, inbox: Inbox{Pool: pool}, pool: pool, typ: "test." + letters(12)}
}

func (w *world) event(t *testing.T, value string) Envelope {
	t.Helper()
	env, err := New(uuid.NewString(), w.typ, 1, "aggregate-1", 1, map[string]string{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// apply writes the event to the applied table in the inbox transaction.
func apply(ctx context.Context, tx pgx.Tx, env Envelope) error {
	var p struct{ Value string }
	if err := env.Decode(&p); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO applied (event_id, value) VALUES ($1, $2)`, env.EventID, p.Value)
	return err
}

func (w *world) consume(t *testing.T, durable string, h Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.bus.Consume(ctx, w.inbox, Subscription{Durable: durable, Types: []string{w.typ}, Handle: h})
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		dctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = w.bus.js.DeleteConsumer(dctx, StreamName, durable)
	})
}

func (w *world) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// An event is applied once even when the broker delivers it twice (a
// republish under another message id, e.g. after the dedup window).
func TestDeliveredOnceDespiteRepublish(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	w.consume(t, durable, apply)
	env := w.event(t, "hello")
	for i := 0; i < 3; i++ {
		if err := w.bus.Publish(t.Context(), env); err != nil { // same msg id: dropped by the broker
			t.Fatal(err)
		}
	}
	data := mustJSON(t, env)
	if _, err := w.bus.js.Publish(t.Context(), Subject(env.Type), data, jetstream.WithMsgID("other-"+env.EventID)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the event to be applied", func() bool {
		return w.count(t, `SELECT count(*) FROM event_inbox WHERE consumer = $1 AND status = 'processed'`, durable) == 1
	})
	time.Sleep(500 * time.Millisecond)
	if n := w.count(t, `SELECT count(*) FROM applied`); n != 1 {
		t.Fatalf("applied %d times", n)
	}
}

// A transient failure is retried; a handler's writes roll back with the
// inbox row, so the retry applies it cleanly.
func TestTransientFailureIsRetriedAndRolledBack(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	var calls atomic.Int32
	w.consume(t, durable, func(ctx context.Context, tx pgx.Tx, env Envelope) error {
		if err := apply(ctx, tx, env); err != nil {
			return err
		}
		if calls.Add(1) < 3 {
			return errors.New("database briefly unavailable")
		}
		return nil
	})
	if err := w.bus.Publish(t.Context(), w.event(t, "retry")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the third attempt to succeed", func() bool {
		return w.count(t, `SELECT count(*) FROM applied`) == 1
	})
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls.Load())
	}
}

// A refusal parks at once (not redelivered); after the fix an operator
// replays it, which applies it and is audited. Retrying forever is bounded.
func TestPermanentFailureParksAndReplayApplies(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	var refuse atomic.Bool
	refuse.Store(true)
	var calls atomic.Int32
	handler := func(ctx context.Context, tx pgx.Tx, env Envelope) error {
		calls.Add(1)
		if refuse.Load() {
			return apperror.Conflict("order was cancelled")
		}
		return apply(ctx, tx, env)
	}
	w.consume(t, durable, handler)
	env := w.event(t, "parked")
	if err := w.bus.Publish(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the event to park", func() bool {
		return w.count(t, `SELECT count(*) FROM event_inbox WHERE status = 'parked' AND event_id = $1`, env.EventID) == 1
	})
	time.Sleep(500 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("a refusal must not be retried, got %d calls", calls.Load())
	}
	parked, err := w.inbox.ListParked(t.Context(), 10)
	if err != nil || len(parked) != 1 || parked[0].LastError == nil || !strings.Contains(*parked[0].LastError, "cancelled") {
		t.Fatalf("parked: %+v %v", parked, err)
	}
	stats, _ := w.inbox.Stats(t.Context())
	if stats["events_parked"] != 1 {
		t.Fatalf("stats: %v", stats)
	}

	admin := uuid.NewString()
	if err := w.inbox.Replay(t.Context(), durable, env.EventID, admin, "still refused", handler); err == nil {
		t.Fatal("a replay that fails changes nothing")
	}
	refuse.Store(false)
	if err := w.inbox.Replay(t.Context(), durable, env.EventID, admin, "order fixed by support", handler); err != nil {
		t.Fatal(err)
	}
	if err := w.inbox.Replay(t.Context(), durable, env.EventID, admin, "again", handler); !errors.Is(err, ErrNotParked) {
		t.Fatalf("replaying twice: %v", err)
	}
	if w.count(t, `SELECT count(*) FROM applied`) != 1 ||
		w.count(t, `SELECT count(*) FROM event_inbox_audit WHERE action = 'event_replayed' AND actor_id = $1`, admin) != 1 {
		t.Fatal("the replay applies once and is audited")
	}
	if _, err := w.pool.Exec(t.Context(), `DELETE FROM event_inbox_audit`); err == nil {
		t.Fatal("audit is append-only")
	}
}

func TestEndlessTransientFailureParksAfterMaxAttempts(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	var calls atomic.Int32
	w.consume(t, durable, func(context.Context, pgx.Tx, Envelope) error {
		calls.Add(1)
		return errors.New("downstream unavailable")
	})
	env := w.event(t, "never")
	if err := w.bus.Publish(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	eventually(t, "parking after the last attempt", func() bool {
		return w.count(t, `SELECT count(*) FROM event_inbox WHERE status = 'parked' AND attempts = $1`, MaxAttempts) == 1
	})
	time.Sleep(500 * time.Millisecond)
	if calls.Load() != MaxAttempts {
		t.Fatalf("expected %d attempts, got %d", MaxAttempts, calls.Load())
	}
	if err := w.inbox.Discard(t.Context(), durable, env.EventID, uuid.NewString(), "obsolete"); err != nil {
		t.Fatal(err)
	}
	if w.count(t, `SELECT count(*) FROM event_inbox WHERE status = 'discarded'`) != 1 {
		t.Fatal("discarded")
	}
}

func TestMalformedMessageIsParkedNotLooped(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	var calls atomic.Int32
	w.consume(t, durable, func(context.Context, pgx.Tx, Envelope) error { calls.Add(1); return nil })
	if _, err := w.bus.js.Publish(t.Context(), Subject(w.typ), []byte(`{"not":"an envelope"}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the malformed message to park", func() bool {
		return w.count(t, `SELECT count(*) FROM event_inbox WHERE consumer = $1 AND status = 'parked' AND event_type = 'invalid.envelope'`, durable) == 1
	})
	if calls.Load() != 0 {
		t.Fatal("the handler never sees a malformed message")
	}
}

// Consumers stop on shutdown and pick up where they left off.
func TestConsumerResumesAfterRestart(t *testing.T) {
	w := newWorld(t)
	durable := "t-" + letters(8)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.bus.Consume(ctx, w.inbox, Subscription{Durable: durable, Types: []string{w.typ}, Handle: apply})
	}()
	if err := w.bus.Publish(t.Context(), w.event(t, "one")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first event", func() bool { return w.count(t, `SELECT count(*) FROM applied`) == 1 })
	cancel()
	wg.Wait()
	for i := 0; i < 3; i++ { // published while the consumer is down
		if err := w.bus.Publish(t.Context(), w.event(t, "later")); err != nil {
			t.Fatal(err)
		}
	}
	w.consume(t, durable, apply)
	eventually(t, "the backlog after restart", func() bool { return w.count(t, `SELECT count(*) FROM applied`) == 4 })
}

func TestEnvelopeValidation(t *testing.T) {
	if _, err := New("id-1", "Vendor.Status", 1, "a", 1, map[string]any{}); err == nil {
		t.Fatal("type must be lowercase producer.event")
	}
	if _, err := New("", "vendor.status_changed", 1, "a", 1, map[string]any{}); err == nil {
		t.Fatal("event id required")
	}
	big := strings.Repeat("x", MaxPayloadBytes)
	if _, err := New("id-1", "vendor.status_changed", 1, "a", 1, map[string]string{"x": big}); err == nil {
		t.Fatal("payload is bounded")
	}
	env, err := New("id-1", "vendor.status_changed", 1, "a", 1, map[string]string{"x": "y"})
	if err != nil || env.Producer != "vendor" || env.WithCorrelation("bad id!").CorrelationID != "id-1" ||
		env.WithCorrelation("req-12345678").CorrelationID != "req-12345678" {
		t.Fatalf("env %+v %v", env, err)
	}
	if !IsPermanent(apperror.Validation("x")) || IsPermanent(errors.New("io")) || IsPermanent(apperror.Forbidden("x")) || !IsPermanent(Permanent(errors.New("x"))) {
		t.Fatal("permanent classification")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
