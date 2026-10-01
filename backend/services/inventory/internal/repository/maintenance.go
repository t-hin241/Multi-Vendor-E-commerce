package repository

import (
	"context"
	"errors"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/inventory/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Maintenance struct{ Pool *pgxpool.Pool }
type OutboxEvent = domain.OutboxEvent

var ErrNoInventoryEvent = errors.New("no due inventory event")

func (r Maintenance) Dispatch(ctx context.Context, publish func(context.Context, OutboxEvent) error) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.Pool)
		var e OutboxEvent
		err := q.QueryRow(ctx, `SELECT id,order_id,event_type FROM inventory_outbox WHERE delivered_at IS NULL AND attempts<10 AND next_attempt_at<=now() ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&e.ID, &e.OrderID, &e.Type)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoInventoryEvent
		}
		if err != nil {
			return err
		}
		if err := publish(ctx, e); err != nil {
			_, saveErr := q.Exec(ctx, `UPDATE inventory_outbox SET attempts=attempts+1,last_error='order_delivery_failed',next_attempt_at=now()+interval '1 second'*least(300,power(2,attempts)) WHERE id=$1`, e.ID)
			return saveErr
		}
		_, err = q.Exec(ctx, `UPDATE inventory_outbox SET delivered_at=now(),last_error=NULL WHERE id=$1`, e.ID)
		return err
	})
}
func (r Maintenance) Stats(ctx context.Context) (map[string]int64, error) {
	rows, err := r.Pool.Query(ctx, `SELECT 'held',count(*) FROM reservation_operations WHERE status='held' UNION ALL
 SELECT 'overdue',count(*) FROM reservation_operations WHERE status='held' AND expires_at<=now() UNION ALL
 SELECT 'legacy_held',count(*) FROM reservation_operations WHERE status='held' AND legacy UNION ALL
 SELECT 'expiry_parked',count(*) FROM reservation_operations WHERE status='held' AND expiry_attempts>=10 UNION ALL
 SELECT 'cache_pending',count(*) FROM inventory_stock_outbox UNION ALL
 SELECT 'cache_parked',count(*) FROM inventory_stock_outbox WHERE attempts>=10 UNION ALL
 SELECT 'events_pending',count(*) FROM inventory_outbox WHERE delivered_at IS NULL UNION ALL
 SELECT 'events_parked',count(*) FROM inventory_outbox WHERE delivered_at IS NULL AND attempts>=10 UNION ALL
 SELECT 'order_mismatches',count(*) FROM reservation_operations WHERE reconciliation_issue IS NOT NULL UNION ALL
 SELECT 'reserved_mismatches',count(*) FROM inventory_items i LEFT JOIN (SELECT inventory_item_id,sum(quantity)::numeric held FROM stock_reservations WHERE status='active' GROUP BY inventory_item_id)s ON s.inventory_item_id=i.id WHERE i.reserved_quantity::numeric<>coalesce(s.held,0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
func (r Maintenance) Candidates(ctx context.Context) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `SELECT order_id FROM reservation_operations WHERE reconciled_at<now()-interval '1 minute' ORDER BY reconciled_at,order_id LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (r Maintenance) Observe(ctx context.Context, id, status, issue string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE reservation_operations SET reconciled_at=now(),reconciliation_issue=NULLIF($3,'') WHERE order_id=$1 AND status=$2`, id, status, issue)
	return err
}

type OperationIssue = domain.OperationIssue

func (r Maintenance) Issues(ctx context.Context, limit, offset int) ([]OperationIssue, error) {
	rows, err := r.Pool.Query(ctx, `SELECT order_id,status,coalesce(reconciliation_issue,CASE WHEN legacy THEN 'legacy_review' ELSE 'overdue' END),legacy FROM reservation_operations WHERE reconciliation_issue IS NOT NULL OR (status='held' AND (legacy OR expires_at<=now())) ORDER BY created_at,order_id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationIssue{}
	for rows.Next() {
		var v OperationIssue
		if err := rows.Scan(&v.OrderID, &v.Status, &v.Issue, &v.Legacy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Maintenance) Repair(ctx context.Context, id, actor, action, reason string) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		repo := NewReservationRepository(r.Pool)
		if err := repo.lockOperation(ctx, id); err != nil {
			return err
		}
		o, err := repo.Operation(ctx, id)
		if err != nil {
			return err
		}
		q := connection(ctx, r.Pool)
		switch action {
		case "release_cancelled":
			if o.Status == "held" {
				if err := repo.finish(ctx, o, "released"); err != nil {
					return err
				}
			} else if o.Status != "released" && o.Status != "expired" {
				return apperror.Conflict("Committed stock cannot be released")
			}
		case "adopt_legacy":
			tag, err := q.Exec(ctx, `UPDATE reservation_operations SET legacy=false,reconciled_at='epoch' WHERE order_id=$1 AND status='held' AND expires_at>clock_timestamp()`, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return apperror.Conflict("Only an unexpired held operation can be adopted")
			}
		case "replay":
			if _, err := q.Exec(ctx, `UPDATE inventory_outbox SET attempts=0,next_attempt_at=now(),delivered_at=NULL,last_error=NULL WHERE order_id=$1`, id); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `UPDATE reservation_operations SET expiry_attempts=0,expiry_next_at=now(),reconciled_at='epoch' WHERE order_id=$1`, id); err != nil {
				return err
			}
		default:
			return apperror.Validation("Invalid repair action")
		}
		_, err = q.Exec(ctx, `INSERT INTO inventory_operation_audit(order_id,entity_id,actor_user_id,action,reason,request_id) VALUES($1,$2,$3,$4,$5,$6)`, id, id, actor, action, reason, middleware.CorrelationID(ctx))
		return err
	})
}

func (r Maintenance) SyncStock(ctx context.Context, invalidate func(context.Context, []string) error) error {
	return (Transactions{Pool: r.Pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.Pool)
		rows, err := q.Query(ctx, `SELECT variant_id FROM inventory_stock_outbox WHERE attempts<10 AND next_attempt_at<=now() ORDER BY next_attempt_at,variant_id LIMIT 100 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := invalidate(ctx, ids); err != nil {
			_, e := q.Exec(ctx, `UPDATE inventory_stock_outbox SET attempts=attempts+1,next_attempt_at=now()+interval '1 second'*least(300,power(2,attempts)) WHERE variant_id=ANY($1)`, ids)
			return e
		}
		_, err = q.Exec(ctx, `DELETE FROM inventory_stock_outbox WHERE variant_id=ANY($1)`, ids)
		return err
	})
}
