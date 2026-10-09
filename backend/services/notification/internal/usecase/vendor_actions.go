package usecase

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/notification/internal/adapter"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
)

// VendorActionStore keeps AF-08 shop work events and notice preferences.
type VendorActionStore interface {
	Record(ctx context.Context, a *domain.VendorAction) (*domain.VendorAction, bool, error)
	Find(ctx context.Context, id string) (*domain.VendorAction, error)
	Claim(ctx context.Context, lease time.Duration) (*domain.VendorAction, error)
	Complete(ctx context.Context, id string, attempt int, res repository.Resolution) error
	Requeue(ctx context.Context, id string) (*domain.VendorAction, error)
	List(ctx context.Context, f repository.VendorActionFilter, limit, offset int) ([]*domain.VendorAction, int64, error)
	Counts(ctx context.Context) (map[string]int64, error)
	RecordAudit(ctx context.Context, actorID, action, id string, reason *string, changes map[string]any) error
	OptIns(ctx context.Context, userIDs []string) (map[string][]string, error)
	Preference(ctx context.Context, userID string) (*repository.Preference, error)
	SavePreference(ctx context.Context, userID string, categories []string, expectedVersion int64) (*repository.Preference, error)
}

// RecipientDirectory asks Vendor who may receive a shop's notices.
type RecipientDirectory interface {
	NotificationRecipients(ctx context.Context, vendorID, purpose string) (*adapter.NoticeRecipients, error)
}

// VendorActionUseCase turns a producer's "this shop has work" event into
// notices (AF-08). Recording never calls anything outside PostgreSQL, so a
// Vendor outage cannot fail or slow the producer; a worker resolves the
// recipients afterwards, writes one notification per person and hands them
// to the normal delivery queue.
type VendorActionUseCase struct {
	Store         VendorActionStore
	Notifications *NotificationUseCase
	Directory     RecipientDirectory
	Tx            Transactor
	// Enabled is FEATURE_VENDOR_ACTION_NOTICES_ENABLED. Off: nothing new is
	// recorded or resolved and preferences answer feature_disabled.
	Enabled bool
	Log     zerolog.Logger
	Now     func() time.Time
	// Wake, when set (buffered, size 1), starts a resolution round at once
	// after a new event instead of at the next tick.
	Wake chan struct{}
}

var errNoticesDisabled = &apperror.Error{Code: "feature_disabled", Message: "Shop work notices are not enabled", Status: http.StatusNotFound}

func (uc *VendorActionUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

// Record stores a producer's event once; a repeat returns the stored one
// (duplicate = true). It is answered at once; resolution runs later.
func (uc *VendorActionUseCase) Record(ctx context.Context, r domain.VendorActionRequest) (*domain.VendorAction, bool, error) {
	if !uc.Enabled {
		return nil, false, errNoticesDisabled
	}
	a, err := domain.NewVendorAction(r, uc.now())
	if err != nil {
		return nil, false, apperror.Validation(err.Error())
	}
	stored, created, err := uc.Store.Record(ctx, a)
	if err != nil {
		return nil, false, apperror.Internal(err)
	}
	if created {
		uc.Log.Info().Str("vendor_action_id", stored.ID).Str("source", stored.Source).Str("event_id", stored.EventID).
			Str("vendor_id", stored.VendorID).Str("action_kind", stored.ActionKind).Msg("vendor_action_recorded")
		uc.kick()
	}
	return stored, !created, nil
}

func (uc *VendorActionUseCase) kick() {
	if uc.Wake == nil {
		return
	}
	select {
	case uc.Wake <- struct{}{}:
	default:
	}
}

// resolveLease is how long one resolution holds its event (Vendor call
// plus the write); a worker that stops frees it when the lease ends.
const resolveLease = 30 * time.Second

// ResolveNext resolves the next due event, if any, and reports whether it
// found one. An error means the database could not be used at all.
func (uc *VendorActionUseCase) ResolveNext(ctx context.Context) (bool, error) {
	a, err := uc.Store.Claim(ctx, resolveLease)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	log := uc.Log.With().Str("vendor_action_id", a.ID).Str("vendor_id", a.VendorID).Str("action_kind", a.ActionKind).Int("attempt", a.Attempts).Logger()
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	list, err := uc.Directory.NotificationRecipients(dctx, a.VendorID, a.Purpose)
	cancel()
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == apperror.CodeNotFound {
			return true, uc.finish(ctx, a, repository.Resolution{Status: domain.VendorActionNoRecipient, Error: ptr("shop not found")}, nil, log)
		}
		res := repository.Resolution{Status: domain.VendorActionPending, Error: ptr("recipient lookup unavailable"),
			NextAttemptAt: uc.now().Add(domain.Backoff(a.Attempts))}
		if a.Attempts >= domain.MaxVendorActionAttempts {
			res.Status = domain.VendorActionParked
		}
		return true, uc.finish(ctx, a, res, nil, log)
	}
	candidates := make([]domain.Candidate, 0, len(list.Recipients))
	staff := []string{}
	for _, r := range list.Recipients {
		candidates = append(candidates, domain.Candidate{UserID: r.UserID, Role: r.Role})
		if r.Role == "staff" {
			staff = append(staff, r.UserID)
		}
	}
	optIns, err := uc.Store.OptIns(ctx, staff)
	if err != nil {
		return true, err // the lease ends and the event is taken again
	}
	users := domain.SelectRecipients(a.Purpose, candidates, optIns)
	version := list.PermissionVersion
	if len(users) == 0 {
		return true, uc.finish(ctx, a, repository.Resolution{Status: domain.VendorActionNoRecipient, PermissionVersion: &version,
			Error: ptr("no owner or opted-in staff may receive this notice")}, nil, log)
	}
	return true, uc.finish(ctx, a, repository.Resolution{Status: domain.VendorActionResolved, RecipientUserIDs: users, PermissionVersion: &version}, users, log)
}

// finish records the attempt; for a resolved event it writes one
// notification per user in the same transaction, then queues their
// delivery jobs (a lost job is recovered from PostgreSQL as usual).
func (uc *VendorActionUseCase) finish(ctx context.Context, a *domain.VendorAction, res repository.Resolution, users []string, log zerolog.Logger) error {
	if res.NextAttemptAt.IsZero() {
		res.NextAttemptAt = uc.now()
	}
	var created []*domain.Notification
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		created = created[:0]
		for _, user := range users {
			correlation := ""
			if a.CorrelationID != nil {
				correlation = *a.CorrelationID
			}
			n, err := domain.NewNotification(domain.Request{EventID: a.EventID, Source: a.Source, UserID: user, Type: a.Notice(),
				ReferenceID: a.ReferenceID, CorrelationID: correlation}, uc.now())
			if err != nil {
				return err
			}
			stored, isNew, err := uc.Notifications.Store.Insert(ctx, n)
			if err != nil {
				return err
			}
			if isNew {
				created = append(created, stored)
			}
		}
		return uc.Store.Complete(ctx, a.ID, a.Attempts, res)
	})
	if errors.Is(err, repository.ErrStale) {
		log.Warn().Msg("vendor_action_taken_by_another_worker")
		return nil
	}
	if err != nil {
		return err
	}
	for _, n := range created {
		uc.Notifications.enqueue(ctx, n.ID, n.Attempts+1, n.NextAttemptAt)
	}
	switch res.Status {
	case domain.VendorActionResolved:
		log.Info().Int("recipients", len(users)).Int("notices_created", len(created)).Msg("vendor_action_resolved")
	case domain.VendorActionPending:
		log.Warn().Time("next_attempt_at", res.NextAttemptAt).Msg("vendor_action_retry")
	default:
		log.Error().Str("status", string(res.Status)).Str("reason", deref(res.Error)).Msg("vendor_action_needs_review")
	}
	return nil
}

func ptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Run resolves due events every 5 seconds (and at once after a new one)
// and logs the report every minute. It stops with ctx.
func (uc *VendorActionUseCase) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	report := time.NewTicker(time.Minute)
	defer tick.Stop()
	defer report.Stop()
	for {
		uc.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-uc.Wake:
		case <-report.C:
			uc.report(ctx)
		}
	}
}

// drain resolves up to 50 events per round, so one round stays short.
func (uc *VendorActionUseCase) drain(ctx context.Context) {
	for range 50 {
		if ctx.Err() != nil {
			return
		}
		found, err := uc.ResolveNext(ctx)
		if err != nil {
			uc.Log.Error().Err(err).Msg("vendor_action_resolution_failed")
			return
		}
		if !found {
			return
		}
	}
}

func (uc *VendorActionUseCase) report(ctx context.Context) {
	counts, err := uc.Store.Counts(ctx)
	if err != nil {
		uc.Log.Error().Err(err).Msg("vendor_action_report_failed")
		return
	}
	event := uc.Log.Info()
	if counts["vendor_actions_no_recipient"] > 0 || counts["vendor_actions_parked"] > 0 || counts["vendor_actions_pending_over_15m"] > 0 {
		event = uc.Log.Warn()
	}
	for k, v := range counts {
		event = event.Int64(k, v)
	}
	event.Msg("vendor_action_report")
}

var vendorActionStatuses = map[string]bool{"": true, "pending": true, "resolving": true, "resolved": true, "no_recipient": true, "parked": true}

// List is the admin view of shop notice events ("chưa xác định người
// nhận": status no_recipient or parked).
func (uc *VendorActionUseCase) List(ctx context.Context, f repository.VendorActionFilter, limit, offset int) ([]*domain.VendorAction, int64, error) {
	if !vendorActionStatuses[f.Status] {
		return nil, 0, apperror.Validation("status must be pending, resolving, resolved, no_recipient or parked")
	}
	if f.VendorID != "" {
		if _, err := uuid.Parse(f.VendorID); err != nil {
			return nil, 0, apperror.Validation("vendor_id must be a shop id")
		}
	}
	items, total, err := uc.Store.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	return items, total, nil
}

// Counts is the resolution report for the admin.
func (uc *VendorActionUseCase) Counts(ctx context.Context, adminID string) (map[string]int64, error) {
	if err := uc.Notifications.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	counts, err := uc.Store.Counts(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return counts, nil
}

// Retry sends an event that needs review (no recipient, parked) back to
// resolution with fresh attempts, e.g. after the owner's account was
// unlocked. It needs a verified admin and a reason and is audited; the
// notices already written are kept (dedup per person).
func (uc *VendorActionUseCase) Retry(ctx context.Context, adminID, id, reason string) (*domain.VendorAction, error) {
	why := strings.TrimSpace(reason)
	if why == "" || len(why) > 500 {
		return nil, apperror.Validation("A reason of at most 500 characters is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.Validation("Invalid event id")
	}
	if err := uc.Notifications.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var out *domain.VendorAction
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		before, err := uc.Store.Find(ctx, id)
		if err != nil {
			return err
		}
		if !before.NeedsReview() {
			return apperror.Conflict("Only an event without recipients or parked can be retried")
		}
		if out, err = uc.Store.Requeue(ctx, id); err != nil {
			return err
		}
		return uc.Store.RecordAudit(ctx, adminID, "vendor_action_retried", id, &why,
			map[string]any{"status": []any{before.Status, out.Status}, "attempts": []any{before.Attempts, out.Attempts}})
	})
	switch {
	case err == nil:
	case errors.Is(err, repository.ErrNotFound):
		return nil, apperror.NotFound("Event not found")
	case errors.Is(err, repository.ErrStale):
		return nil, apperror.Conflict("This event changed meanwhile; reload and try again")
	default:
		var app *apperror.Error
		if errors.As(err, &app) {
			return nil, app
		}
		return nil, apperror.Internal(err)
	}
	uc.Log.Info().Str("vendor_action_id", id).Str("admin_id", adminID).Msg("vendor_action_retried")
	uc.kick()
	return out, nil
}

// Preferences is a person's shop notice opt-ins.
type Preferences struct {
	Optional []string
	Version  int64
}

// GetPreferences returns userID's own opt-ins.
func (uc *VendorActionUseCase) GetPreferences(ctx context.Context, userID string) (*Preferences, error) {
	if !uc.Enabled {
		return nil, errNoticesDisabled
	}
	p, err := uc.Store.Preference(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return &Preferences{Optional: p.Categories, Version: p.Version}, nil
}

// UpdatePreferences replaces userID's own opt-ins if expectedVersion is
// still current (0 for the first save). It changes what staff receive; the
// owner receives every category whatever is stored.
func (uc *VendorActionUseCase) UpdatePreferences(ctx context.Context, userID string, categories []string, expectedVersion int64) (*Preferences, error) {
	if !uc.Enabled {
		return nil, errNoticesDisabled
	}
	cats, err := domain.NormalizeVendorCategories(categories)
	if err != nil {
		return nil, apperror.Validation(err.Error())
	}
	if expectedVersion < 0 {
		return nil, apperror.Validation("expected_version must not be negative")
	}
	p, err := uc.Store.SavePreference(ctx, userID, cats, expectedVersion)
	if errors.Is(err, repository.ErrStale) {
		return nil, apperror.Conflict("Your preferences changed meanwhile; reload and try again")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	uc.Log.Info().Str("user_id", userID).Strs("categories", p.Categories).Msg("notification_preferences_updated")
	return &Preferences{Optional: p.Categories, Version: p.Version}, nil
}
