package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/inventory/internal/domain"
)

type ReservationRepository struct {
	pool *pgxpool.Pool
}

func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{pool: pool}
}

// lockKey is what a reservation line is actually locked by: its variant id
// when it has one, else its product id — matching the same "variant_id
// when present, else product_id" branching used everywhere else in this
// table's queries.
func lockKey(line domain.ReservationLine) string {
	if line.VariantID != nil {
		return *line.VariantID
	}
	return line.ProductID
}

// ReserveAtomic holds stock for every line of a checkout in a single
// database transaction: either all lines are reserved, or none are. Rows
// are locked with SELECT ... FOR UPDATE in a fixed order (by variant id when
// a line has one, else by product id) so two concurrent checkouts that both
// touch the same products/variants can never deadlock each other, and
// neither can push available stock negative.
func (r *ReservationRepository) ReserveAtomic(ctx context.Context, orderID string, lines []domain.ReservationLine) ([]*domain.Reservation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	sorted := make([]domain.ReservationLine, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return lockKey(sorted[i]) < lockKey(sorted[j]) })

	expiresAt := time.Now().Add(domain.ReservationTTL)
	reservations := make([]*domain.Reservation, 0, len(sorted))

	for _, line := range sorted {
		var itemID string
		var available int64
		var err error
		// A product with variant-scoped stock rows has no product-level
		// row at all, so the two cases must be looked up separately —
		// "WHERE product_id = $1" alone (the pre-variant query) would
		// otherwise match zero, one, or an arbitrary row once a product
		// has per-variant inventory rows instead of a single bare one.
		if line.VariantID != nil {
			err = tx.QueryRow(ctx,
				`SELECT id, available_quantity FROM inventory_items WHERE variant_id = $1 FOR UPDATE`,
				*line.VariantID,
			).Scan(&itemID, &available)
		} else {
			err = tx.QueryRow(ctx,
				`SELECT id, available_quantity FROM inventory_items WHERE product_id = $1 AND variant_id IS NULL FOR UPDATE`,
				line.ProductID,
			).Scan(&itemID, &available)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &ErrProductNotStocked{ProductID: line.ProductID}
			}
			return nil, err
		}

		if available < line.Quantity {
			return nil, &ErrInsufficientStock{ProductID: line.ProductID, Available: available, Requested: line.Quantity}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items SET available_quantity = available_quantity - $1, reserved_quantity = reserved_quantity + $1, updated_at = now() WHERE id = $2`,
			line.Quantity, itemID,
		); err != nil {
			return nil, err
		}

		reservation := &domain.Reservation{
			InventoryItemID: itemID,
			ProductID:       line.ProductID,
			VariantID:       line.VariantID,
			OrderID:         orderID,
			Quantity:        line.Quantity,
			Status:          domain.ReservationActive,
			ExpiresAt:       expiresAt,
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO stock_reservations (inventory_item_id, order_id, quantity, expires_at)
			 VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
			itemID, orderID, line.Quantity, expiresAt,
		).Scan(&reservation.ID, &reservation.CreatedAt)
		if err != nil {
			return nil, err
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (inventory_item_id, change_quantity, reason, reference_id) VALUES ($1, $2, 'reservation_held', $3)`,
			itemID, -line.Quantity, orderID,
		); err != nil {
			return nil, err
		}

		reservations = append(reservations, reservation)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return reservations, nil
}

// ReleaseByOrderID returns every active reservation for orderID back to
// available stock. It is idempotent: releasing an order with no active
// reservations (already released, or never reserved) is a no-op success,
// since a cancel/payment-failure consumer may be retried.
func (r *ReservationRepository) ReleaseByOrderID(ctx context.Context, orderID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT id, inventory_item_id, quantity FROM stock_reservations WHERE order_id = $1 AND status = 'active' ORDER BY inventory_item_id FOR UPDATE`,
		orderID,
	)
	if err != nil {
		return err
	}

	type activeReservation struct {
		id     string
		itemID string
		qty    int64
	}
	var active []activeReservation
	for rows.Next() {
		var a activeReservation
		if err := rows.Scan(&a.id, &a.itemID, &a.qty); err != nil {
			rows.Close()
			return err
		}
		active = append(active, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, a := range active {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items SET available_quantity = available_quantity + $1, reserved_quantity = reserved_quantity - $1, updated_at = now() WHERE id = $2`,
			a.qty, a.itemID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'released', updated_at = now() WHERE id = $1`, a.id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (inventory_item_id, change_quantity, reason, reference_id) VALUES ($1, $2, 'reservation_released', $3)`,
			a.itemID, a.qty, orderID,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// CommitByOrderID turns every active reservation for orderID into a
// permanent sale: available_quantity was already decremented when the
// reservation was made, so only the reserved bucket moves — the stock is
// now genuinely gone, not just held. Like ReleaseByOrderID, it is
// idempotent: an order with no active reservations left (already committed,
// or never reserved) is a no-op success, since a retried payment webhook
// may call it more than once.
func (r *ReservationRepository) CommitByOrderID(ctx context.Context, orderID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT id, inventory_item_id, quantity FROM stock_reservations WHERE order_id = $1 AND status = 'active' ORDER BY inventory_item_id FOR UPDATE`,
		orderID,
	)
	if err != nil {
		return err
	}

	type activeReservation struct {
		id     string
		itemID string
		qty    int64
	}
	var active []activeReservation
	for rows.Next() {
		var a activeReservation
		if err := rows.Scan(&a.id, &a.itemID, &a.qty); err != nil {
			rows.Close()
			return err
		}
		active = append(active, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, a := range active {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items SET reserved_quantity = reserved_quantity - $1, updated_at = now() WHERE id = $2`,
			a.qty, a.itemID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'committed', updated_at = now() WHERE id = $1`, a.id); err != nil {
			return err
		}
		// change_quantity is 0: committing doesn't move stock (available
		// was already debited at reservation time) — this row exists purely
		// as an audit trail of when a hold became a final sale.
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_movements (inventory_item_id, change_quantity, reason, reference_id) VALUES ($1, 0, 'sale_committed', $2)`,
			a.itemID, orderID,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
