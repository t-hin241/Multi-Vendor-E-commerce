package repository_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/taskqueue"
	"shopee/backend/services/notification/internal/usecase"
)

// redisQueue is a real Asynq queue on the test Redis, under a queue name of
// its own (removed afterwards).
func redisQueue(t *testing.T) (*taskqueue.Queue, *asynq.Inspector, string) {
	t.Helper()
	raw := os.Getenv("NOTIFICATION_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("NOTIFICATION_TEST_REDIS_URL is not configured")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal("invalid test redis configuration")
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("test redis: %v", err)
	}
	name := "notification_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	inspector := asynq.NewInspectorFromRedisClient(rdb)
	t.Cleanup(func() {
		_ = inspector.DeleteQueue(name, true)
		_ = rdb.Close()
	})
	return taskqueue.New(rdb, name, 10*time.Second), inspector, name
}

func (e *env) asynqUseCase(q *taskqueue.Queue) *usecase.NotificationUseCase {
	return usecase.NewNotificationUseCase(usecase.Deps{Store: e.repo, Tx: repository.Transactions{Pool: e.pool}, Identity: e.ids,
		Sender: e.mail, Roles: roles{}, Log: zerolog.Nop(), SendTimeout: 2 * time.Second, Queue: q})
}

func (e *env) waitSent(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if e.state(t, id).Status == domain.StatusSent {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("notification %s was not delivered, status %s", id, e.state(t, id).Status)
}

// NTF-01 with Asynq: a request is delivered by the queue's workers; jobs
// lost with Redis data are queued again from PostgreSQL and delivered once.
func TestAsynqDeliversAndRecoversLostJobs(t *testing.T) {
	e := newEnv(t)
	q, inspector, name := redisQueue(t)
	uc := e.asynqUseCase(q)
	accept := func(event string) *domain.Notification {
		n, _, err := uc.Accept(t.Context(), domain.Request{EventID: event, Source: "order", UserID: e.buyer, Type: domain.TypeOrderPaid, ReferenceID: "order-1"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Recorded and queued while no worker runs, then Redis loses its data.
	lost := accept("effect-redis-lost")
	if info, err := inspector.GetTaskInfo(name, lost.ID+":1"); err != nil || info.State != asynq.TaskStatePending {
		t.Fatalf("expected a pending job for the first attempt: %v %v", info, err)
	}
	if _, err := inspector.DeleteAllPendingTasks(name); err != nil {
		t.Fatal(err)
	}
	if stats, err := q.Stats(t.Context()); err != nil || stats["queue_size"] != 0 {
		t.Fatalf("expected an empty queue: %v %v", stats, err)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE notifications SET next_attempt_at = now() - interval '2 minutes' WHERE id = $1`, lost.ID); err != nil {
		t.Fatal(err)
	}
	if added, err := uc.Recover(t.Context()); err != nil || added != 1 {
		t.Fatalf("expected the lost job queued again: %d %v", added, err)
	}
	if added, err := uc.Recover(t.Context()); err != nil || added != 0 {
		t.Fatalf("a queued job must be kept, not duplicated: %d %v", added, err)
	}

	stop, err := q.Start(uc.Deliver, 2, 2*time.Second, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	e.waitSent(t, lost.ID)
	fresh := accept("effect-redis-fresh")
	e.waitSent(t, fresh.ID)
	if e.mail.count() != 2 {
		t.Fatalf("expected exactly two emails, got %d", e.mail.count())
	}
	counts, err := uc.Report(t.Context())
	if err != nil || counts["queue_unreachable"] != 0 || counts["sent_24h"] != 2 {
		t.Fatalf("report: %v %v", counts, err)
	}
}

// A job the queue gave up on (archived) is replaced when PostgreSQL says
// the attempt is still due; a job still waiting is kept; a later attempt
// is scheduled, not run at once.
func TestAsynqJobIdentity(t *testing.T) {
	q, inspector, name := redisQueue(t)
	ctx := t.Context()
	id := uuid.NewString()
	if added, err := q.Enqueue(ctx, id, 1, time.Now()); err != nil || !added {
		t.Fatalf("enqueue: %v %v", added, err)
	}
	if added, err := q.Enqueue(ctx, id, 1, time.Now()); err != nil || added {
		t.Fatalf("a job for the same attempt must be kept: %v %v", added, err)
	}
	if err := inspector.ArchiveTask(name, id+":1"); err != nil {
		t.Fatal(err)
	}
	if added, err := q.Enqueue(ctx, id, 1, time.Now()); err != nil || !added {
		t.Fatalf("an archived job must be replaced: %v %v", added, err)
	}
	if info, err := inspector.GetTaskInfo(name, id+":1"); err != nil || info.State != asynq.TaskStatePending {
		t.Fatalf("expected the replacement pending: %v %v", info, err)
	}
	if added, err := q.Enqueue(ctx, id, 2, time.Now().Add(time.Hour)); err != nil || !added {
		t.Fatalf("enqueue retry: %v %v", added, err)
	}
	if info, err := inspector.GetTaskInfo(name, id+":2"); err != nil || info.State != asynq.TaskStateScheduled {
		t.Fatalf("a retry must wait for its time: %v %v", info, err)
	}
	if string(mustPayload(t, inspector, name, id+":2")) != `{"notification_id":"`+id+`","attempt":2}` {
		t.Fatal("a job carries only the notification id and attempt")
	}
	stats, err := q.Stats(ctx)
	if err != nil || stats["queue_size"] != 2 || stats["queue_archived"] != 0 {
		t.Fatalf("stats: %v %v", stats, err)
	}
}

func mustPayload(t *testing.T, inspector *asynq.Inspector, queue, id string) []byte {
	t.Helper()
	info, err := inspector.GetTaskInfo(queue, id)
	if err != nil {
		t.Fatal(err)
	}
	return info.Payload
}
