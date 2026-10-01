package repository_test

import (
	"context"
	"errors"
	"fmt"
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
	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/sender"
	"shopee/backend/services/notification/internal/usecase"
)

func notificationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("NOTIFICATION_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("NOTIFICATION_TEST_DATABASE_URL is not configured")
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
	schema := "notification_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

type identity struct {
	users map[string]*adapter.UserSnapshot
}

func (i identity) GetUser(_ context.Context, id string) (*adapter.UserSnapshot, error) {
	if u, ok := i.users[id]; ok {
		return u, nil
	}
	return nil, apperror.NotFound("User not found")
}

// mailbox records sends; fail decides each send's error.
type mailbox struct {
	mu   sync.Mutex
	sent []sender.Email
	fail func() error
}

func (m *mailbox) Send(_ context.Context, e sender.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		if err := m.fail(); err != nil {
			return err
		}
	}
	m.sent = append(m.sent, e)
	return nil
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type roles struct{ deny bool }

func (r roles) RequireRole(context.Context, string, string) error {
	if r.deny {
		return apperror.Forbidden("Active account with required role needed")
	}
	return nil
}

// jobs stands in for the Asynq queue (taskqueue): one job per
// "<notification>:<attempt>", a job already queued is kept, and it can be
// down (Redis unavailable) or lose everything (Redis data loss).
type jobs struct {
	mu     sync.Mutex
	queued map[string][2]any
	down   bool
}

func (j *jobs) Enqueue(_ context.Context, id string, attempt int, _ time.Time) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.down {
		return false, errors.New("queue unavailable")
	}
	key := fmt.Sprintf("%s:%d", id, attempt)
	if _, ok := j.queued[key]; ok {
		return false, nil
	}
	j.queued[key] = [2]any{id, attempt}
	return true, nil
}

func (j *jobs) Stats(context.Context) (map[string]int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.down {
		return nil, errors.New("queue unavailable")
	}
	return map[string]int64{"queue_size": int64(len(j.queued))}, nil
}

func (j *jobs) size() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.queued)
}

// take removes and returns every queued job (as the workers would pick
// them up; tests move next_attempt_at instead of waiting).
func (j *jobs) take() [][2]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([][2]any, 0, len(j.queued))
	for k, v := range j.queued {
		out = append(out, v)
		delete(j.queued, k)
	}
	return out
}

type env struct {
	pool  *pgxpool.Pool
	repo  *repository.NotificationRepository
	mail  *mailbox
	ids   identity
	jobs  *jobs
	buyer string
}

func newEnv(t *testing.T) *env {
	pool := notificationDB(t)
	e := &env{pool: pool, repo: repository.NewNotificationRepository(pool), mail: &mailbox{}, jobs: &jobs{queued: map[string][2]any{}}, buyer: uuid.NewString()}
	e.ids = identity{users: map[string]*adapter.UserSnapshot{e.buyer: {ID: e.buyer, Email: "buyer@example.invalid", FullName: "Test Buyer", Active: true}}}
	return e
}

// useCase is a fresh instance, like a restarted service.
func (e *env) useCase(r roles) *usecase.NotificationUseCase {
	return usecase.NewNotificationUseCase(usecase.Deps{Store: e.repo, Tx: repository.Transactions{Pool: e.pool}, Identity: e.ids,
		Sender: e.mail, Roles: r, Log: zerolog.Nop(), SendTimeout: 2 * time.Second, Queue: e.jobs})
}

// run executes every queued job once with a fresh service instance and
// returns how many there were.
func (e *env) run(t *testing.T) int {
	t.Helper()
	queued := e.jobs.take()
	uc := e.useCase(roles{})
	for _, j := range queued {
		if err := uc.Deliver(t.Context(), j[0].(string), j[1].(int)); err != nil {
			t.Fatal(err)
		}
	}
	return len(queued)
}

func (e *env) accept(t *testing.T, eventID string) *domain.Notification {
	t.Helper()
	n, _, err := e.useCase(roles{}).Accept(t.Context(), domain.Request{EventID: eventID, Source: "order", UserID: e.buyer, Type: domain.TypeOrderPaid, ReferenceID: "order-1"})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) state(t *testing.T, id string) *domain.Notification {
	t.Helper()
	n, err := e.repo.FindByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) due(t *testing.T, id string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE notifications SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// NTF-01: a repeated event (even concurrent) is recorded once, and the
// record exists before anything is sent.
func TestAcceptRecordsOncePerEvent(t *testing.T) {
	e := newEnv(t)
	uc := e.useCase(roles{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	duplicates := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, dup, err := uc.Accept(t.Context(), domain.Request{EventID: "effect-1", Source: "order", UserID: e.buyer, Type: domain.TypeOrderPaid, ReferenceID: "order-1"})
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			if dup {
				duplicates++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if n := e.count(t, `SELECT count(*) FROM notifications WHERE status = 'pending'`); n != 1 || duplicates != 9 {
		t.Fatalf("expected one pending record and nine duplicates, got %d and %d", n, duplicates)
	}
	if e.mail.count() != 0 {
		t.Fatal("accepting must not send")
	}
	if e.jobs.size() != 1 {
		t.Fatalf("expected one delivery job, got %d", e.jobs.size())
	}
	if _, _, err := uc.Accept(t.Context(), domain.Request{UserID: e.buyer, Type: "marketing", ReferenceID: "x"}); err == nil {
		t.Fatal("an unknown type must be refused")
	}
}

// Provider outage: the request survives, is retried with backoff by a
// restarted service, and is parked after its last attempt; nothing loops.
func TestOutageRetriesAcrossRestartsThenParks(t *testing.T) {
	e := newEnv(t)
	n := e.accept(t, "effect-outage")
	e.mail.fail = func() error { return sender.Transient("mail connection failed") }
	for attempt := 1; attempt <= domain.DefaultMaxAttempts; attempt++ {
		e.due(t, n.ID)
		if handled := e.run(t); handled != 1 {
			t.Fatalf("attempt %d: expected one queued job, got %d", attempt, handled)
		}
		got := e.state(t, n.ID)
		if got.Attempts != attempt {
			t.Fatalf("attempt count %d, want %d", got.Attempts, attempt)
		}
		if attempt < domain.DefaultMaxAttempts && (got.Status != domain.StatusPending || !got.NextAttemptAt.After(time.Now())) {
			t.Fatalf("attempt %d: expected a scheduled retry, got %s at %s", attempt, got.Status, got.NextAttemptAt)
		}
	}
	if got := e.state(t, n.ID); got.Status != domain.StatusParked || got.FailReason == nil || *got.FailReason != "mail connection failed" {
		t.Fatalf("expected parked with the reason, got %+v", got)
	}
	e.due(t, n.ID)
	if e.jobs.size() != 0 {
		t.Fatal("a parked notification is not retried automatically")
	}
	if err := e.useCase(roles{}).Deliver(t.Context(), n.ID, domain.DefaultMaxAttempts+1); err != nil || e.state(t, n.ID).Status != domain.StatusParked {
		t.Fatal("a stray job must not take a parked notification")
	}
	if added, err := e.useCase(roles{}).Recover(t.Context()); err != nil || added != 0 {
		t.Fatalf("recovery must not requeue a parked notification: %d %v", added, err)
	}
	if a := e.count(t, `SELECT count(*) FROM notification_attempts WHERE notification_id = $1`, n.ID); a != domain.DefaultMaxAttempts {
		t.Fatalf("expected every attempt recorded, got %d", a)
	}
	counts, err := e.repo.Counts(t.Context())
	if err != nil || counts["parked"] != 1 {
		t.Fatalf("parked must be observable: %v %v", counts, err)
	}

	// Delivery after recovery works for a new event.
	e.mail.fail = nil
	ok := e.accept(t, "effect-after")
	e.run(t)
	got := e.state(t, ok.ID)
	if got.Status != domain.StatusSent || got.SentAt == nil || got.RecipientMasked == nil || *got.RecipientMasked != "b***@example.invalid" {
		t.Fatalf("expected sent with a masked recipient, got %+v", got)
	}
	// The address itself is stored nowhere in Notification.
	if n := e.count(t, `SELECT count(*) FROM notifications WHERE recipient_masked LIKE '%buyer@%' OR fail_reason LIKE '%@%'`) +
		e.count(t, `SELECT count(*) FROM notification_attempts WHERE error LIKE '%@%'`); n != 0 {
		t.Fatal("the recipient address must not be stored")
	}
}

func TestPermanentFailuresStopAtOnce(t *testing.T) {
	e := newEnv(t)
	unknown := uuid.NewString()
	n, _, _ := e.useCase(roles{}).Accept(t.Context(), domain.Request{EventID: "e1", Source: "vendor", UserID: unknown, Type: domain.TypeVendorApproved, ReferenceID: "shop-1"})
	refused := e.accept(t, "e2")
	e.mail.fail = func() error { return sender.Permanent("mail recipient rejected") }
	if handled := e.run(t); handled != 2 {
		t.Fatalf("expected two jobs, got %d", handled)
	}
	if e.jobs.size() != 0 {
		t.Fatal("a permanent failure must not queue another attempt")
	}
	for id, reason := range map[string]string{n.ID: "recipient not found", refused.ID: "mail recipient rejected"} {
		got := e.state(t, id)
		if got.Status != domain.StatusFailed || got.Attempts != 1 || *got.FailReason != reason {
			t.Fatalf("expected failed after one attempt with %q, got %+v", reason, got)
		}
	}
}

// A worker that stops after sending but before recording is replaced once
// its lease ends: Recover queues the next attempt, the message is sent again
// (at-least-once), the attempts stay bounded, and the stopped worker cannot
// overwrite the new outcome.
func TestCrashAfterSendIsBounded(t *testing.T) {
	e := newEnv(t)
	n := e.accept(t, "effect-crash")
	claimed, err := e.repo.ClaimTask(t.Context(), n.ID, 1, time.Minute) // the worker that "crashes"
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	e.run(t) // the same job delivered twice (Asynq re-runs an orphaned job)
	if e.mail.count() != 0 {
		t.Fatal("a leased notification must not be taken twice")
	}
	if added, _ := e.useCase(roles{}).Recover(t.Context()); added != 0 {
		t.Fatal("a notification under lease must not be requeued")
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE notifications SET lease_until = now() - interval '1 second' WHERE id = $1`, n.ID); err != nil {
		t.Fatal(err)
	}
	if added, err := e.useCase(roles{}).Recover(t.Context()); err != nil || added != 1 {
		t.Fatalf("expected the next attempt queued after the lease, got %d %v", added, err)
	}
	e.run(t)
	if got := e.state(t, n.ID); got.Status != domain.StatusSent || got.Attempts != 2 || e.mail.count() != 1 {
		t.Fatalf("expected sent on the second attempt, got %+v", got)
	}
	err = e.repo.Finish(t.Context(), n.ID, claimed.Attempts, repository.Outcome{Status: domain.StatusPending, NextAttemptAt: time.Now()})
	if !errors.Is(err, repository.ErrStale) {
		t.Fatalf("the stopped worker must not overwrite the outcome, got %v", err)
	}

	// Stopping during the last attempt parks instead of trying forever.
	last := e.accept(t, "effect-crash-last")
	e.jobs.take()
	if _, err := e.pool.Exec(t.Context(), `UPDATE notifications SET status = 'sending', attempts = max_attempts, lease_until = now() - interval '1 second' WHERE id = $1`, last.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.useCase(roles{}).Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := e.state(t, last.ID); got.Status != domain.StatusParked || e.jobs.size() != 0 {
		t.Fatalf("expected parked and nothing queued, got %s", got.Status)
	}
}

// NTF-01: a job lost in Redis (queue down when the request came, Redis
// data loss) is queued again from PostgreSQL, once, and delivered once.
func TestLostJobsAreRecoveredFromPostgres(t *testing.T) {
	e := newEnv(t)
	e.jobs.down = true
	n := e.accept(t, "effect-lost") // the producer still gets its answer
	if got := e.state(t, n.ID); got.Status != domain.StatusPending {
		t.Fatalf("the request must be recorded, got %s", got.Status)
	}
	counts, err := e.useCase(roles{}).Report(t.Context())
	if err != nil || counts["queue_unreachable"] != 1 || counts["pending"] != 1 {
		t.Fatalf("an unreachable queue must be reported: %v %v", counts, err)
	}
	e.jobs.down = false
	if added, _ := e.useCase(roles{}).Recover(t.Context()); added != 0 {
		t.Fatal("a notification that is not late yet is left to its own job")
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE notifications SET next_attempt_at = now() - interval '2 minutes' WHERE id = $1`, n.ID); err != nil {
		t.Fatal(err)
	}
	e.jobs.down = true
	if _, err := e.useCase(roles{}).Recover(t.Context()); err == nil {
		t.Fatal("recovery must report the queue being down")
	}
	e.jobs.down = false
	if added, err := e.useCase(roles{}).Recover(t.Context()); err != nil || added != 1 {
		t.Fatalf("expected the lost job queued again, got %d %v", added, err)
	}
	if added, _ := e.useCase(roles{}).Recover(t.Context()); added != 0 {
		t.Fatal("a job already queued must not be queued twice")
	}
	e.run(t)
	e.run(t)
	if got := e.state(t, n.ID); got.Status != domain.StatusSent || got.Attempts != 1 || e.mail.count() != 1 {
		t.Fatalf("expected one delivery, got %+v and %d sends", got, e.mail.count())
	}
}

// NTF-04: an admin retry needs a verified admin and a reason, is audited,
// adds a few attempts, and cannot be repeated on a queued notification.
func TestAdminRetryIsVerifiedAuditedAndBounded(t *testing.T) {
	e := newEnv(t)
	n := e.accept(t, "effect-retry")
	e.mail.fail = func() error { return sender.Permanent("mail recipient rejected") }
	e.run(t)
	admin := uuid.NewString()
	if _, err := e.useCase(roles{deny: true}).Retry(t.Context(), uuid.NewString(), n.ID, "fixed"); err == nil {
		t.Fatal("a non-admin must be refused")
	}
	if _, err := e.useCase(roles{}).Retry(t.Context(), admin, n.ID, " "); err == nil {
		t.Fatal("a reason is required")
	}
	retried, err := e.useCase(roles{}).Retry(t.Context(), admin, n.ID, "Buyer corrected the mailbox")
	if err != nil || retried.Status != domain.StatusPending || retried.MaxAttempts < retried.Attempts+3 {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	if _, err := e.useCase(roles{}).Retry(t.Context(), admin, n.ID, "again"); err == nil {
		t.Fatal("a queued notification cannot be retried again")
	}
	if a := e.count(t, `SELECT count(*) FROM notification_admin_audit WHERE entity_id = $1 AND actor_id = $2 AND action = 'notification_retried'`, n.ID, admin); a != 1 {
		t.Fatalf("expected one audit row, got %d", a)
	}
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM notification_admin_audit`); err == nil {
		t.Fatal("audit rows must not be deletable")
	}
	e.mail.fail = nil
	if handled := e.run(t); handled != 1 {
		t.Fatalf("the retry must queue one job, got %d", handled)
	}
	if got := e.state(t, n.ID); got.Status != domain.StatusSent {
		t.Fatalf("expected sent after the retry, got %s", got.Status)
	}
	attempts, err := e.repo.Attempts(t.Context(), n.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != "failed" || attempts[1].Outcome != "sent" {
		t.Fatalf("attempt history: %+v %v", attempts, err)
	}
	if purged, err := e.repo.PurgeAttempts(t.Context(), time.Now().Add(time.Hour)); err != nil || purged != 2 {
		t.Fatalf("purge: %d %v", purged, err)
	}
}
