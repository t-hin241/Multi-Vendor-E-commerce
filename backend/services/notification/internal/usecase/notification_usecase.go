// Package usecase runs Notification's workflow: a producing service's
// event is recorded as a pending notification (deduplicated) and answered
// at once, and a job for its first attempt is queued (Asynq). The job's
// worker resolves the recipient, renders the template and sends, recording
// every attempt in PostgreSQL, queueing the next attempt with backoff after
// a transient failure and stopping on a permanent one. PostgreSQL is the
// record: a job lost in Redis is queued again from it. A failure here never
// reaches the order, payment or vendor decision that caused the event.
package usecase

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/sender"
)

type Deps struct {
	Store    Store
	Tx       Transactor
	Identity IdentityGateway
	Sender   sender.Sender
	Roles    RoleVerifier
	Log      zerolog.Logger
	Now      func() time.Time
	// SendTimeout bounds one provider call.
	SendTimeout time.Duration
	// Queue carries delivery jobs; without it nothing is delivered, but
	// requests are still recorded (and queued by Recover once it is set).
	Queue TaskQueue
	// Inbox (AF-09, FEATURE_NOTIFICATION_INBOX_ENABLED) receives an item for
	// every new notice, in the same transaction; nil writes none.
	Inbox InboxWriter
}

// InboxWriter adds a person's inbox item once per source event and kind.
type InboxWriter interface {
	Add(ctx context.Context, item *domain.InboxItem) error
}

type NotificationUseCase struct{ Deps }

func NewNotificationUseCase(d Deps) *NotificationUseCase {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.SendTimeout <= 0 {
		d.SendTimeout = 20 * time.Second
	}
	return &NotificationUseCase{Deps: d}
}

// Accept records a notification request once and returns it; a repeat of
// the same event returns the existing record (duplicate = true). Nothing is
// sent here, so a mail outage never slows or fails the producer.
func (uc *NotificationUseCase) Accept(ctx context.Context, r domain.Request) (*domain.Notification, bool, error) {
	if _, err := uuid.Parse(r.UserID); err != nil {
		return nil, false, apperror.Validation("user_id must be a user id")
	}
	n, err := domain.NewNotification(r, uc.Now().UTC())
	if err != nil {
		return nil, false, apperror.Validation(err.Error())
	}
	var stored *domain.Notification
	var created bool
	err = uc.inTx(ctx, func(ctx context.Context) error {
		var err error
		stored, created, err = uc.record(ctx, n)
		return err
	})
	if err != nil {
		return nil, false, apperror.Internal(err)
	}
	if created {
		uc.Log.Info().Str("notification_id", stored.ID).Str("event_id", stored.EventID).Str("source", stored.Source).
			Str("type", string(stored.Type)).Msg("notification_accepted")
		uc.enqueue(ctx, stored.ID, stored.Attempts+1, stored.NextAttemptAt)
	}
	return stored, !created, nil
}

// record stores a notice once and, the first time, its inbox item in the
// same transaction (the caller's when there is one): the inbox never shows
// a notice that was not recorded, and a repeat never adds a second item.
func (uc *NotificationUseCase) record(ctx context.Context, n *domain.Notification) (*domain.Notification, bool, error) {
	stored, created, err := uc.Store.Insert(ctx, n)
	if err != nil || !created || uc.Inbox == nil {
		return stored, created, err
	}
	item, ok, err := domain.NewInboxItem(stored)
	if err != nil || !ok {
		return stored, created, err
	}
	return stored, created, uc.Inbox.Add(ctx, item)
}

// inTx runs fn in a transaction when the inbox needs one (two writes);
// otherwise directly, as before AF-09.
func (uc *NotificationUseCase) inTx(ctx context.Context, fn func(context.Context) error) error {
	if uc.Inbox == nil || uc.Tx == nil {
		return fn(ctx)
	}
	return uc.Tx.Run(ctx, fn)
}

// enqueue queues a delivery job. A failure is only logged: the record is
// already in PostgreSQL and Recover queues it again.
func (uc *NotificationUseCase) enqueue(ctx context.Context, id string, attempt int, at time.Time) {
	if uc.Queue == nil {
		return
	}
	qctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if _, err := uc.Queue.Enqueue(qctx, id, attempt, at); err != nil {
		uc.Log.Warn().Err(err).Str("notification_id", id).Int("attempt", attempt).Msg("notification_enqueue_failed")
	}
}

// lease is how long an attempt holds its notification; a worker that
// stops mid-attempt frees it when the lease ends.
func (uc *NotificationUseCase) lease() time.Duration { return uc.SendTimeout + 30*time.Second }

// Deliver runs the job for attempt number attempt of notification id. A job
// that is duplicate, stale or no longer needed (already sent, retried by an
// admin, taken by another worker) does nothing. An error means the attempt
// could not even be claimed (database unavailable): the queue retries the
// job, and Recover covers it if the queue gives up.
func (uc *NotificationUseCase) Deliver(ctx context.Context, id string, attempt int) error {
	n, err := uc.Store.ClaimTask(ctx, id, attempt, uc.lease())
	if errors.Is(err, repository.ErrNotFound) {
		uc.Log.Debug().Str("notification_id", id).Int("attempt", attempt).Msg("notification_job_skipped")
		return nil
	}
	if err != nil {
		return err
	}
	uc.deliver(ctx, n)
	return nil
}

// deliver makes one attempt for a claimed notification, records it and
// queues the next attempt after a transient failure.
func (uc *NotificationUseCase) deliver(ctx context.Context, n *domain.Notification) {
	started := uc.Now()
	outcome := uc.attempt(ctx, n)
	outcome.Duration = uc.Now().Sub(started)
	log := uc.Log.With().Str("notification_id", n.ID).Str("type", string(n.Type)).Int("attempt", n.Attempts).
		Str("status", string(outcome.Status)).Logger()
	if outcome.Reason != nil {
		log = log.With().Str("reason", *outcome.Reason).Logger()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := uc.Store.Finish(finishCtx, n.ID, n.Attempts, outcome); err != nil {
		// The attempt stays "sending" until its lease ends; Recover then
		// queues it again (at-least-once).
		log.Error().Err(err).Msg("notification_finish_failed")
		return
	}
	switch outcome.Status {
	case domain.StatusSent:
		log.Info().Dur("latency", uc.Now().Sub(n.CreatedAt)).Msg("notification_sent")
	case domain.StatusPending:
		log.Warn().Time("next_attempt_at", outcome.NextAttemptAt).Msg("notification_retry")
		uc.enqueue(finishCtx, n.ID, n.Attempts+1, outcome.NextAttemptAt)
	default:
		log.Error().Msg("notification_not_delivered")
	}
}

// recoverGrace is how late a pending notification may be before its job is
// presumed lost; a queued job normally runs within seconds of its time.
const recoverGrace = time.Minute

// Recover parks notifications whose worker stopped during their last
// attempt and queues a job again for every notification that is overdue
// (job lost with Redis data, failed enqueue, job given up by the queue,
// worker stopped mid-attempt). A job still queued is kept, so nothing is
// queued twice. It returns how many jobs it added.
func (uc *NotificationUseCase) Recover(ctx context.Context) (int, error) {
	if parked, err := uc.Store.ParkStuck(ctx); err != nil {
		return 0, err
	} else if parked > 0 {
		uc.Log.Error().Int64("notifications", parked).Msg("notification_parked_after_stopped_attempt")
	}
	if uc.Queue == nil {
		return 0, nil
	}
	due, err := uc.Store.Unqueued(ctx, recoverGrace, 500)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, d := range due {
		ok, err := uc.Queue.Enqueue(ctx, d.ID, d.Attempt, uc.Now())
		if err != nil {
			return added, err
		}
		if ok {
			added++
		}
	}
	if added > 0 {
		uc.Log.Warn().Int("jobs", added).Msg("notification_jobs_requeued_from_postgres")
	}
	return added, nil
}

func (uc *NotificationUseCase) attempt(ctx context.Context, n *domain.Notification) repository.Outcome {
	user, err := uc.Identity.GetUser(ctx, n.UserID)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == apperror.CodeNotFound {
			return uc.fail(n, sender.Permanent("recipient not found"))
		}
		return uc.fail(n, sender.Transient("recipient lookup unavailable"))
	}
	if !user.Active {
		return uc.fail(n, sender.Permanent("recipient account is locked"))
	}
	masked := domain.MaskEmail(user.Email)
	subject, body, err := domain.Render(n, user.FullName)
	if err != nil {
		return uc.fail(n, sender.Permanent("no template for this notification"))
	}
	sendCtx, cancel := context.WithTimeout(ctx, uc.SendTimeout)
	defer cancel()
	err = uc.Sender.Send(sendCtx, sender.Email{ToEmail: user.Email, Subject: subject, Body: body, Type: string(n.Type), Reference: n.ReferenceID})
	if err != nil {
		o := uc.fail(n, sender.Classify(err))
		o.RecipientMasked = &masked
		return o
	}
	return repository.Outcome{Status: domain.StatusSent, RecipientMasked: &masked, NextAttemptAt: uc.Now().UTC()}
}

// fail decides what a failed attempt leads to: stop on a permanent
// failure, park after the last attempt, otherwise retry with backoff.
func (uc *NotificationUseCase) fail(n *domain.Notification, f *sender.Failure) repository.Outcome {
	reason := f.Reason
	if f.Uncertain {
		reason += " (may have been delivered)"
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	now := uc.Now().UTC()
	switch {
	case f.Permanent:
		return repository.Outcome{Status: domain.StatusFailed, Reason: &reason, NextAttemptAt: now}
	case n.Attempts >= n.MaxAttempts:
		return repository.Outcome{Status: domain.StatusParked, Reason: &reason, NextAttemptAt: now}
	}
	return repository.Outcome{Status: domain.StatusPending, Reason: &reason, NextAttemptAt: now.Add(domain.Backoff(n.Attempts))}
}

// Report is the delivery health the worker logs and the admin sees:
// PostgreSQL counts plus the job queue (queue_unreachable = 1 when Redis
// does not answer; requests are still recorded meanwhile).
func (uc *NotificationUseCase) Report(ctx context.Context) (map[string]int64, error) {
	counts, err := uc.Store.Counts(ctx)
	if err != nil || uc.Queue == nil {
		return counts, err
	}
	stats, err := uc.Queue.Stats(ctx)
	if err != nil {
		uc.Log.Warn().Err(err).Msg("notification_queue_unreachable")
		counts["queue_unreachable"] = 1
		return counts, nil
	}
	counts["queue_unreachable"] = 0
	for k, v := range stats {
		counts[k] = v
	}
	return counts, nil
}

// PurgeAttempts removes attempt history older than retention.
func (uc *NotificationUseCase) PurgeAttempts(ctx context.Context, retention time.Duration) (int64, error) {
	return uc.Store.PurgeAttempts(ctx, uc.Now().Add(-retention))
}

func (uc *NotificationUseCase) requireAdmin(ctx context.Context, adminID string) error {
	if uc.Roles == nil {
		return apperror.Internal(errors.New("admin verification is not configured"))
	}
	if err := uc.Roles.RequireRole(ctx, adminID, "admin"); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) {
			return app
		}
		return apperror.Internal(err)
	}
	return nil
}

var listStatuses = map[string]bool{"": true, "pending": true, "sending": true, "sent": true, "failed": true, "parked": true}

// List is the admin's delivery view (recipient masked).
func (uc *NotificationUseCase) List(ctx context.Context, f repository.Filter, limit, offset int) ([]*domain.Notification, int64, error) {
	if !listStatuses[f.Status] {
		return nil, 0, apperror.Validation("status must be pending, sending, sent, failed or parked")
	}
	if f.Type != "" && !domain.KnownType(domain.Type(f.Type)) {
		return nil, 0, apperror.Validation("unknown notification type")
	}
	if f.UserID != "" {
		if _, err := uuid.Parse(f.UserID); err != nil {
			return nil, 0, apperror.Validation("user_id must be a user id")
		}
	}
	items, total, err := uc.Store.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return items, total, nil
}

func (uc *NotificationUseCase) Attempts(ctx context.Context, id string) ([]*domain.Attempt, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.Validation("Invalid notification id")
	}
	items, err := uc.Store.Attempts(ctx, id)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return items, nil
}

// Operations is the delivery report for the admin (re-verified).
func (uc *NotificationUseCase) Operations(ctx context.Context, adminID string) (map[string]int64, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	counts, err := uc.Report(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return counts, nil
}

// retryAttempts is how many more attempts an admin retry allows.
const retryAttempts = 3

// Retry puts a failed or parked notification back in the queue. It needs
// a verified admin and a reason, and is audited with the change; retrying
// one already queued or sent is refused, so a resend cannot multiply it.
func (uc *NotificationUseCase) Retry(ctx context.Context, adminID, id, reason string) (*domain.Notification, error) {
	why := strings.TrimSpace(reason)
	if why == "" || len(why) > 500 {
		return nil, apperror.Validation("A reason of at most 500 characters is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.Validation("Invalid notification id")
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if uc.Tx == nil {
		return nil, apperror.Internal(errors.New("transactions are not configured"))
	}
	var out *domain.Notification
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		before, err := uc.Store.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if !before.Retryable() {
			return apperror.Conflict("Only a failed or parked notification can be retried")
		}
		if out, err = uc.Store.Requeue(ctx, id, retryAttempts); err != nil {
			return err
		}
		return uc.Store.RecordAudit(ctx, adminID, "notification_retried", id, &why,
			map[string]any{"status": []any{before.Status, out.Status}, "max_attempts": []any{before.MaxAttempts, out.MaxAttempts}})
	})
	switch {
	case err == nil:
	case errors.Is(err, repository.ErrNotFound):
		return nil, apperror.NotFound("Notification not found")
	case errors.Is(err, repository.ErrStale):
		return nil, apperror.Conflict("This notification changed meanwhile; reload and try again")
	default:
		var app *apperror.Error
		if errors.As(err, &app) {
			return nil, app
		}
		return nil, apperror.Internal(err)
	}
	uc.Log.Info().Str("notification_id", id).Str("admin_id", adminID).Msg("notification_retried")
	uc.enqueue(ctx, out.ID, out.Attempts+1, out.NextAttemptAt)
	return out, nil
}
