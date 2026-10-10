package casesla

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/config"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
)

type Publisher interface {
	Publish(context.Context, eventbus.Envelope) error
}
type Worker struct {
	Store  Store
	Owner  string
	Config config.CaseSLA
	Roles  Roles
	// Permissions (AF-19), when set, also requires recipients to hold
	// support.manage; one without it is skipped like an inactive admin.
	Permissions adminaccess.Checker
	Publisher   Publisher
	// WaitingOnShop (PW-009), when set, is called in the scan transaction
	// once per deadline version and kind ("reminder", "overdue") of a stage
	// waiting on the shop; the owner queues the shop's notice in its outbox.
	WaitingOnShop func(ctx context.Context, tx pgx.Tx, i *Item, kind string) error
	Log           zerolog.Logger
	Now           func() time.Time
}

func (w Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w Worker) Run(ctx context.Context) {
	interval := w.Config.PollInterval
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if e := w.Tick(ctx); e != nil && ctx.Err() == nil {
			w.Log.Error().Err(e).Msg("case_sla_worker_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w Worker) Tick(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := w.now()
	var dbNow time.Time
	if e := w.Store.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); e != nil {
		return e
	}
	if d := now.Sub(dbNow); d > 30*time.Second || d < -30*time.Second {
		w.Log.Error().Msg("case_sla_clock_skew")
		return errors.New("case SLA clock differs from database by over 30 seconds")
	}
	// Serialize each batch with SKIP LOCKED; receipts and notices commit together.
	err := pgx.BeginFunc(ctx, w.Store.Pool, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT payload FROM case_sla_work_items WHERE active AND next_check_at<=$1 ORDER BY next_check_at,id LIMIT 100 FOR UPDATE SKIP LOCKED`, now)
		if e != nil {
			return e
		}
		var items []*Item
		for rows.Next() {
			i, e := readItem(rows)
			if e != nil {
				rows.Close()
				return e
			}
			items = append(items, i)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		// Verify each distinct recipient once per batch; Identity errors fail
		// closed and leave deadlines/receipts retryable on the next poll.
		checked := map[string]bool{}
		active := func(id string) (bool, error) {
			if ok, seen := checked[id]; seen {
				return ok, nil
			}
			if w.Roles == nil {
				return false, errors.New("SLA recipient verification unavailable")
			}
			e := w.Roles.RequireRole(ctx, id, "admin")
			if e == nil && w.Permissions != nil {
				_, e = w.Permissions.Require(ctx, id, adminaccess.SupportManage)
			}
			var app *apperror.Error
			if e != nil && !(errors.As(e, &app) && app.Status == 403) {
				return false, e
			}
			checked[id] = e == nil
			return e == nil, nil
		}
		for _, i := range items {
			if i.AssigneeID != nil {
				ok, e := active(*i.AssigneeID)
				if e != nil {
					return e
				}
				if !ok {
					before := *i
					i.AssigneeID = nil
					i.Version++
					if e = audit(ctx, tx, i, "system", "unassigned", "Assignee is no longer an active admin", &before); e != nil {
						return e
					}
				}
			}
			due := i.EffectiveDueAt()
			if !now.Before(due) && i.BreachedAt == nil {
				at := due
				i.BreachedAt = &at
			}
			if !now.Before(due.Add(72 * time.Hour)) {
				i.NeedsAttention = true
			}
			if w.Config.Enabled {
				for _, kind := range i.Notices(now) {
					if e := w.tellShop(ctx, tx, i, kind); e != nil {
						return e
					}
					recipients := slices.Clone(w.Config.OnCallIDs)
					if i.AssigneeID != nil {
						recipients = []string{*i.AssigneeID}
					}
					if kind != "reminder" {
						recipients = append(recipients, w.Config.EscalationIDs...)
					}
					slices.Sort(recipients)
					recipients = slices.Compact(recipients)
					valid := []string{}
					for _, id := range recipients {
						ok, e := active(id)
						if e != nil {
							return e
						}
						if ok {
							valid = append(valid, id)
						}
					}
					if len(valid) == 0 {
						w.Log.Error().Str("work_item_id", i.ID).Msg("case_sla_no_active_recipient")
						continue
					}
					tag, e := tx.Exec(ctx, `INSERT INTO case_sla_reminder_receipts(work_item_id,deadline_version,reminder_kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, i.ID, i.DeadlineVersion, kind)
					if e != nil {
						return e
					}
					if tag.RowsAffected() == 0 {
						continue
					}
					for _, id := range valid {
						typ := w.Owner + ".work_item_overdue"
						if kind == "reminder" {
							typ = w.Owner + ".work_item_reminder"
						}
						env, e := eventbus.New(eventbus.NewID(), typ, events.V1, i.ResourceID, i.Version, events.WorkItemNotice{ResourceType: i.ResourceType, ResourceID: i.ResourceID, SLAVersion: i.PolicyVersion, DeadlineVersion: i.DeadlineVersion, Stage: i.Stage, DueAt: due, Kind: kind, UserID: id})
						if e != nil {
							return e
						}
						env = env.WithCorrelation(i.ID)
						data, e := json.Marshal(env)
						if e != nil {
							return e
						}
						if _, e = tx.Exec(ctx, `INSERT INTO case_sla_notice_outbox(id,work_item_id,envelope) VALUES($1,$2,$3)`, env.EventID, i.ID, data); e != nil {
							return e
						}
					}
				}
			}
			if e = save(ctx, tx, i); e != nil {
				return e
			}
			interval := w.Config.PollInterval
			if interval <= 0 {
				interval = time.Minute
			}
			if _, e = tx.Exec(ctx, `UPDATE case_sla_work_items SET next_check_at=$2 WHERE id=$1`, i.ID, now.Add(interval)); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan case SLA: %w", err)
	}
	if err = w.report(ctx, now); err != nil {
		return err
	}
	if w.Config.Enabled {
		return w.deliver(ctx, now)
	}
	return nil
}

// tellShop hands a reminder or overdue deadline of a stage waiting on the
// shop to the owner, once per deadline version (receipt "shop_<kind>").
// Escalations stay with the marketplace.
func (w Worker) tellShop(ctx context.Context, tx pgx.Tx, i *Item, kind string) error {
	if w.WaitingOnShop == nil || i.WaitingOn != "vendor" || (kind != "reminder" && kind != "overdue") {
		return nil
	}
	tag, e := tx.Exec(ctx, `INSERT INTO case_sla_reminder_receipts(work_item_id,deadline_version,reminder_kind) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, i.ID, i.DeadlineVersion, "shop_"+kind)
	if e != nil || tag.RowsAffected() == 0 {
		return e
	}
	return w.WaitingOnShop(ctx, tx, i, kind)
}

func (w Worker) report(ctx context.Context, now time.Time) error {
	var unassigned, overdue, legacy, extensions, parked int64
	var lag float64
	err := w.Store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM case_sla_work_items WHERE active AND payload->>'assignee_id' IS NULL),
		(SELECT count(*) FROM case_sla_work_items WHERE active AND due_at<=$1),
		(SELECT count(*) FROM case_sla_work_items WHERE active AND payload->>'legacy'='true'),
		(SELECT count(*) FROM case_sla_audit WHERE action='extensions'),
		(SELECT count(*) FROM case_sla_notice_outbox WHERE parked),
		COALESCE((SELECT greatest(0,extract(epoch FROM $1::timestamptz-min(next_attempt_at))) FROM case_sla_notice_outbox WHERE delivered_at IS NULL AND NOT parked),0)`, now).Scan(&unassigned, &overdue, &legacy, &extensions, &parked, &lag)
	if err != nil {
		return err
	}
	w.Log.Info().Bool("shadow", !w.Config.Enabled).Int64("unassigned", unassigned).Int64("overdue", overdue).Int64("legacy", legacy).Int64("extensions", extensions).Int64("parked", parked).Float64("reminder_lag_seconds", lag).Msg("case_sla_report")
	return nil
}

func (w Worker) deliver(ctx context.Context, now time.Time) error {
	if w.Publisher == nil {
		return errors.New("case SLA publisher unavailable")
	}
	// A worker can die during its final attempt. Expiry still exhausts the
	// retry budget instead of granting unlimited additional attempts.
	if _, err := w.Store.Pool.Exec(ctx, `UPDATE case_sla_notice_outbox SET parked=true WHERE delivered_at IS NULL AND attempts>=5 AND (lease_until IS NULL OR lease_until<=$1)`, now); err != nil {
		return err
	}
	for n := 0; n < 100; n++ {
		var data []byte
		var attempts int
		err := w.Store.Pool.QueryRow(ctx, `UPDATE case_sla_notice_outbox SET lease_until=$1::timestamptz+interval '30 seconds',attempts=attempts+1
			WHERE id=(SELECT id FROM case_sla_notice_outbox WHERE delivered_at IS NULL AND NOT parked AND next_attempt_at<=$1
			AND (lease_until IS NULL OR lease_until<=$1) ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING envelope,attempts`, now).Scan(&data, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var env eventbus.Envelope
		if err = json.Unmarshal(data, &env); err != nil {
			return err
		}
		publishErr := w.Publisher.Publish(ctx, env)
		if publishErr == nil {
			_, err = w.Store.Pool.Exec(ctx, `UPDATE case_sla_notice_outbox SET delivered_at=now(),lease_until=NULL WHERE id=$1 AND attempts=$2`, env.EventID, attempts)
		} else {
			w.Log.Warn().Err(publishErr).Str("event_id", env.EventID).Msg("case_sla_notice_retry")
			_, err = w.Store.Pool.Exec(ctx, `UPDATE case_sla_notice_outbox SET lease_until=NULL,parked=($3>=5),next_attempt_at=now()+make_interval(secs=>LEAST(3600,30*power(2,LEAST($3,7)))+random()*10) WHERE id=$1 AND attempts=$2`, env.EventID, attempts, attempts)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
