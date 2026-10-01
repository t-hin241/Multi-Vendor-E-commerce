// Package taskqueue carries Notification's delivery jobs on Asynq (Redis).
// A job is only "make attempt N of notification X" (ids, no address,
// content or secret); the notification, its attempts and its schedule live
// in PostgreSQL, which is how a job lost in Redis is queued again.
package taskqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

const (
	// QueueName is the Asynq queue of notification deliveries.
	QueueName = "notification"
	// TypeDeliver is the job type of one delivery attempt.
	TypeDeliver = "notification:deliver"
	// maxRetry bounds how often Asynq reruns a job that could not even
	// claim its notification (database unavailable). Delivery retries are
	// separate jobs scheduled from PostgreSQL.
	maxRetry = 5
)

type job struct {
	NotificationID string `json:"notification_id"`
	Attempt        int    `json:"attempt"`
}

// Queue enqueues delivery jobs and runs their workers.
type Queue struct {
	rdb       redis.UniversalClient
	client    *asynq.Client
	inspector *asynq.Inspector
	name      string
	// timeout bounds one job (the provider call and recording it).
	timeout time.Duration
}

// New uses rdb (shared, not closed here) and queue name (QueueName in
// production; tests use their own).
func New(rdb redis.UniversalClient, name string, jobTimeout time.Duration) *Queue {
	return &Queue{rdb: rdb, client: asynq.NewClientFromRedisClient(rdb), inspector: asynq.NewInspectorFromRedisClient(rdb), name: name, timeout: jobTimeout}
}

func taskID(id string, attempt int) string { return fmt.Sprintf("%s:%d", id, attempt) }

// Enqueue queues attempt number attempt of notification id to run at at.
// The job id is "<notification>:<attempt>", so a job already queued for the
// same attempt is kept and Enqueue reports false; one the queue gave up on
// (archived) is replaced, since PostgreSQL says the attempt is still due.
func (q *Queue) Enqueue(ctx context.Context, id string, attempt int, at time.Time) (bool, error) {
	added, err := q.enqueue(ctx, id, attempt, at)
	if err == nil || !errors.Is(err, asynq.ErrTaskIDConflict) {
		return added, err
	}
	info, err := q.inspector.GetTaskInfo(q.name, taskID(id, attempt))
	switch {
	case errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound):
		// Finished and removed meanwhile.
	case err != nil:
		return false, fmt.Errorf("taskqueue: inspect job: %w", err)
	case info.State != asynq.TaskStateArchived && info.State != asynq.TaskStateCompleted:
		return false, nil
	default:
		if err := q.inspector.DeleteTask(q.name, info.ID); err != nil && !errors.Is(err, asynq.ErrTaskNotFound) {
			return false, fmt.Errorf("taskqueue: replace archived job: %w", err)
		}
	}
	added, err = q.enqueue(ctx, id, attempt, at)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return false, nil
	}
	return added, err
}

func (q *Queue) enqueue(ctx context.Context, id string, attempt int, at time.Time) (bool, error) {
	payload, err := json.Marshal(job{NotificationID: id, Attempt: attempt})
	if err != nil {
		return false, err
	}
	opts := []asynq.Option{asynq.Queue(q.name), asynq.TaskID(taskID(id, attempt)), asynq.MaxRetry(maxRetry), asynq.Timeout(q.timeout)}
	if at.After(time.Now()) {
		opts = append(opts, asynq.ProcessAt(at))
	}
	if _, err := q.client.EnqueueContext(ctx, asynq.NewTask(TypeDeliver, payload), opts...); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return false, err
		}
		return false, fmt.Errorf("taskqueue: enqueue: %w", err)
	}
	return true, nil
}

// Stats is the queue's share of the delivery report.
func (q *Queue) Stats(context.Context) (map[string]int64, error) {
	info, err := q.inspector.GetQueueInfo(q.name)
	if errors.Is(err, asynq.ErrQueueNotFound) {
		return map[string]int64{"queue_size": 0, "queue_retry": 0, "queue_archived": 0}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("taskqueue: queue info: %w", err)
	}
	return map[string]int64{
		"queue_size":     int64(info.Pending + info.Active + info.Scheduled + info.Retry),
		"queue_retry":    int64(info.Retry),
		"queue_archived": int64(info.Archived),
	}, nil
}

// Deliverer runs one delivery attempt (usecase.NotificationUseCase.Deliver).
type Deliverer func(ctx context.Context, id string, attempt int) error

// Handler routes delivery jobs to deliver.
func Handler(deliver Deliverer) asynq.Handler {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeDeliver, func(ctx context.Context, t *asynq.Task) error {
		var j job
		if err := json.Unmarshal(t.Payload(), &j); err != nil || j.NotificationID == "" || j.Attempt < 1 {
			return fmt.Errorf("malformed notification job: %w", asynq.SkipRetry)
		}
		return deliver(ctx, j.NotificationID, j.Attempt)
	})
	return mux
}

// Start runs concurrency workers for the queue until the returned stop is
// called; stop waits up to shutdownTimeout for running jobs, and a job cut
// short is taken again once its notification's lease ends.
func (q *Queue) Start(deliver Deliverer, concurrency int, shutdownTimeout time.Duration, log zerolog.Logger) (stop func(), err error) {
	srv := asynq.NewServerFromRedisClient(q.rdb, asynq.Config{
		Concurrency:     concurrency,
		Queues:          map[string]int{q.name: 1},
		ShutdownTimeout: shutdownTimeout,
		Logger:          logger{log},
		LogLevel:        asynq.WarnLevel,
		ErrorHandler: asynq.ErrorHandlerFunc(func(_ context.Context, t *asynq.Task, err error) {
			log.Warn().Err(err).Str("job_type", t.Type()).Msg("notification_job_failed")
		}),
	})
	if err := srv.Start(Handler(deliver)); err != nil {
		return nil, fmt.Errorf("taskqueue: start workers: %w", err)
	}
	return srv.Shutdown, nil
}

// logger sends Asynq's own messages to the service log.
type logger struct{ log zerolog.Logger }

func (l logger) Debug(args ...any) { l.log.Debug().Msg(fmt.Sprint(args...)) }
func (l logger) Info(args ...any)  { l.log.Info().Msg(fmt.Sprint(args...)) }
func (l logger) Warn(args ...any)  { l.log.Warn().Msg(fmt.Sprint(args...)) }
func (l logger) Error(args ...any) { l.log.Error().Msg(fmt.Sprint(args...)) }
func (l logger) Fatal(args ...any) { l.log.Fatal().Msg(fmt.Sprint(args...)) }
