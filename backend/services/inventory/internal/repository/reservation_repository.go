package repository

import (
	"context"
	"errors"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReservationRepository struct{ pool *pgxpool.Pool }

func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{pool: pool}
}

func (r *ReservationRepository) lockOperation(ctx context.Context, id string) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,40404))`, id)
	return err
}
func (r *ReservationRepository) Operation(ctx context.Context, id string) (o *domain.Operation, err error) {
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { var e error; o, e = r.operation(ctx, id); return e })
	return
}
func (r *ReservationRepository) operation(ctx context.Context, id string) (*domain.Operation, error) {
	q := connection(ctx, r.pool)
	o := &domain.Operation{OrderID: id, OperationID: id, Items: []*domain.Reservation{}}
	err := q.QueryRow(ctx, `SELECT status,expires_at,legacy FROM reservation_operations WHERE order_id=$1`+lockRow(ctx), id).Scan(&o.Status, &o.ExpiresAt, &o.Legacy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("Reservation operation not found")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT s.id,s.inventory_item_id,i.product_id,i.variant_id,s.quantity,s.status,s.expires_at,s.created_at
 FROM stock_reservations s JOIN inventory_items i ON i.id=s.inventory_item_id WHERE s.order_id=$1 ORDER BY s.inventory_item_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v := &domain.Reservation{OrderID: id}
		if err := rows.Scan(&v.ID, &v.InventoryItemID, &v.ProductID, &v.VariantID, &v.Quantity, &v.Status, &v.ExpiresAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, v)
	}
	return o, rows.Err()
}
func (r *ReservationRepository) ReserveAtomic(ctx context.Context, id string, lines []domain.ReservationLine) (result []*domain.Reservation, err error) {
	normalized, hash, err := domain.NormalizeLines(lines)
	if err != nil {
		return nil, err
	}
	err = (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		q := connection(ctx, r.pool)
		if err := r.lockOperation(ctx, id); err != nil {
			return err
		}
		existing, err := r.Operation(ctx, id)
		if err == nil {
			var stored *string
			if err := q.QueryRow(ctx, `SELECT payload_hash FROM reservation_operations WHERE order_id=$1`, id).Scan(&stored); err != nil {
				return err
			}
			if stored == nil {
				old := make([]domain.ReservationLine, 0, len(existing.Items))
				for _, v := range existing.Items {
					old = append(old, domain.ReservationLine{ProductID: v.ProductID, VariantID: v.VariantID, Quantity: v.Quantity})
				}
				if len(old) == 0 {
					return apperror.Conflict("Reservation was cancelled before stock was held")
				}
				_, legacyHash, err := domain.NormalizeLines(old)
				if err != nil {
					return err
				}
				stored = &legacyHash
			}
			if *stored != hash {
				return apperror.Conflict("Reservation operation already exists with different items")
			}
			result = existing.Items
			return nil
		}
		var app *apperror.Error
		if !errors.As(err, &app) || app.Code != apperror.CodeNotFound {
			return err
		}
		if _, err = q.Exec(ctx, `INSERT INTO reservation_operations(order_id,payload_hash,status,expires_at) VALUES($1,$2,'held',clock_timestamp()+$3::bigint*interval '1 second')`, id, hash, int64(domain.ReservationTTL.Seconds())); err != nil {
			return err
		}
		type resolved struct {
			id   string
			line domain.ReservationLine
		}
		items := make([]resolved, 0, len(normalized))
		for _, line := range normalized {
			var itemID string
			err := q.QueryRow(ctx, `SELECT id FROM inventory_items WHERE product_id=$1 AND variant_id IS NOT DISTINCT FROM $2::uuid`, line.ProductID, line.VariantID).Scan(&itemID)
			if errors.Is(err, pgx.ErrNoRows) {
				return &ErrProductNotStocked{ProductID: line.ProductID}
			}
			if err != nil {
				return err
			}
			items = append(items, resolved{itemID, line})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
		for _, item := range items {
			var available int64
			if err := q.QueryRow(ctx, `SELECT available_quantity FROM inventory_items WHERE id=$1 FOR UPDATE`, item.id).Scan(&available); err != nil {
				return err
			}
			if available < item.line.Quantity {
				return &ErrInsufficientStock{ProductID: item.line.ProductID, Available: available, Requested: item.line.Quantity}
			}
			if _, err := q.Exec(ctx, `UPDATE inventory_items SET available_quantity=available_quantity-$2,reserved_quantity=reserved_quantity+$2,updated_at=now() WHERE id=$1`, item.id, item.line.Quantity); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO stock_reservations(inventory_item_id,order_id,quantity,expires_at) SELECT $1,order_id,$3,expires_at FROM reservation_operations WHERE order_id=$2`, item.id, id, item.line.Quantity); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO stock_movements(inventory_item_id,change_quantity,reserved_change,reason,reference_id,operation_key) VALUES($1,-$2::bigint,$2::bigint,'reservation_held',$3,'hold:'||$3)`, item.id, item.line.Quantity, id); err != nil {
				return err
			}
		}
		o, err := r.Operation(ctx, id)
		if err == nil {
			result = o.Items
		}
		return err
	})
	return
}
func (r *ReservationRepository) ReleaseByOrderID(ctx context.Context, id string) error {
	return r.transition(ctx, id, "released")
}
func (r *ReservationRepository) CommitByOrderID(ctx context.Context, id string) error {
	return r.transition(ctx, id, "committed")
}
func (r *ReservationRepository) transition(ctx context.Context, id, target string) error {
	return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		if err := r.lockOperation(ctx, id); err != nil {
			return err
		}
		q := connection(ctx, r.pool)
		if target == "released" {
			if _, err := q.Exec(ctx, `INSERT INTO reservation_operations(order_id,status,expires_at) VALUES($1,'released',now()) ON CONFLICT DO NOTHING`, id); err != nil {
				return err
			}
		}
		o, err := r.Operation(ctx, id)
		if err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code == apperror.CodeNotFound {
				return apperror.Conflict("No reservation exists for this order")
			}
			return err
		}
		if o.Status == target || target == "released" && o.Status == "expired" {
			return nil
		}
		if o.Status != "held" {
			return apperror.Conflict("Reservation is already " + o.Status)
		}
		if target == "committed" {
			var expired bool
			if err := q.QueryRow(ctx, `SELECT expires_at<=clock_timestamp() FROM reservation_operations WHERE order_id=$1`, id).Scan(&expired); err != nil {
				return err
			}
			if expired {
				return apperror.Conflict("Reservation deadline passed; payment requires reconciliation")
			}
		}
		return r.finish(ctx, o, target)
	})
}
func (r *ReservationRepository) finish(ctx context.Context, o *domain.Operation, target string) error {
	q := connection(ctx, r.pool)
	for _, line := range o.Items {
		if line.Status != domain.ReservationActive {
			return apperror.Conflict("Reservation lines require reconciliation")
		}
		available := line.Quantity
		reason := "reservation_" + target
		if target == "committed" {
			available = 0
			reason = "sale_committed"
		}
		tag, err := q.Exec(ctx, `UPDATE inventory_items SET available_quantity=available_quantity+$2,reserved_quantity=reserved_quantity-$3,updated_at=now() WHERE id=$1 AND reserved_quantity>=$3`, line.InventoryItemID, available, line.Quantity)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return apperror.Conflict("Reserved stock mismatch requires reconciliation")
		}
		if _, err := q.Exec(ctx, `UPDATE stock_reservations SET status=$2,updated_at=now() WHERE id=$1`, line.ID, target); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO stock_movements(inventory_item_id,change_quantity,reserved_change,reason,reference_id,operation_key) VALUES($1,$2,-$3::bigint,$4,$5,$6)`, line.InventoryItemID, available, line.Quantity, reason, o.OrderID, target+":"+o.OrderID); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `UPDATE reservation_operations SET status=$2,updated_at=now(),reconciled_at='epoch' WHERE order_id=$1`, o.OrderID, target); err != nil {
		return err
	}
	if target == "expired" {
		_, err := q.Exec(ctx, `INSERT INTO inventory_outbox(order_id,event_type) VALUES($1,'ReservationExpired') ON CONFLICT DO NOTHING`, o.OrderID)
		return err
	}
	return nil
}

var ErrNoDueReservation = errors.New("no due reservation")

func (r *ReservationRepository) ExpireOne(ctx context.Context) error {
	var id string
	err := (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error {
		err := connection(ctx, r.pool).QueryRow(ctx, `SELECT order_id FROM reservation_operations WHERE status='held' AND NOT legacy AND expires_at<=clock_timestamp() AND expiry_next_at<=now() AND expiry_attempts<10 ORDER BY expires_at,order_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDueReservation
		}
		if err != nil {
			return err
		}
		o, err := r.Operation(ctx, id)
		if err != nil {
			return err
		}
		return r.finish(ctx, o, "expired")
	})
	if err != nil && id != "" {
		_, saveErr := r.pool.Exec(ctx, `UPDATE reservation_operations SET expiry_attempts=expiry_attempts+1,expiry_next_at=now()+interval '1 minute'*least(60,power(2,expiry_attempts)),reconciliation_issue='expiry_failed' WHERE order_id=$1 AND status='held'`, id)
		return errors.Join(err, saveErr)
	}
	return err
}
