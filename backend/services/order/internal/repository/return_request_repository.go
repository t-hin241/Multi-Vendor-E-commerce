package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

var ErrReturnRequestNotFound = errors.New("repository: return request not found")
var ErrReturnNotEligible = errors.New("repository: return request is not eligible")

type ReturnRequestRepository struct{ pool *pgxpool.Pool }

func NewReturnRequestRepository(pool *pgxpool.Pool) *ReturnRequestRepository {
	return &ReturnRequestRepository{pool: pool}
}

const returnRequestColumns = `id, order_id, order_item_id, buyer_id, reason, status, vendor_confirmed_by, vendor_confirmed_at, decided_by, decision_note, decided_at, created_at, updated_at`

func scanReturnRequest(row pgx.Row) (*domain.ReturnRequest, error) {
	var r domain.ReturnRequest
	if err := row.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.BuyerID, &r.Reason, &r.Status, &r.VendorConfirmedBy, &r.VendorConfirmedAt, &r.DecidedBy, &r.DecisionNote, &r.DecidedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReturnRequestNotFound
		}
		return nil, err
	}
	return &r, nil
}

// Create checks ownership and a completed vendor sub-order inside one INSERT.
func (r *ReturnRequestRepository) Create(ctx context.Context, buyerID, orderID, orderItemID, reason string) (*domain.ReturnRequest, error) {
	q := `INSERT INTO return_requests (order_id, order_item_id, buyer_id, reason)
		SELECT o.id, oi.id, o.buyer_id, $4 FROM orders o JOIN order_items oi ON oi.order_id = o.id JOIN vendor_orders vo ON vo.id = oi.vendor_order_id
		WHERE o.id = $1 AND oi.id = $2 AND o.buyer_id = $3 AND vo.status = 'completed'
		RETURNING ` + returnRequestColumns
	request, err := scanReturnRequest(r.pool.QueryRow(ctx, q, orderID, orderItemID, buyerID, reason))
	if errors.Is(err, ErrReturnRequestNotFound) {
		return nil, ErrReturnNotEligible
	}
	return request, err
}
func (r *ReturnRequestRepository) ListByBuyer(ctx context.Context, buyerID string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests WHERE buyer_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, buyerID, limit, offset)
}
func (r *ReturnRequestRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	return r.list(ctx, `SELECT `+returnRequestColumns+` FROM return_requests WHERE ($1='' OR status=$1) ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
}
func (r *ReturnRequestRepository) list(ctx context.Context, q string, args ...any) ([]*domain.ReturnRequest, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ReturnRequest{}
	for rows.Next() {
		item, scanErr := scanReturnRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (r *ReturnRequestRepository) Decide(ctx context.Context, id, adminID string, approve bool, note string) (*domain.ReturnRequest, error) {
	status := domain.ReturnRejected
	if approve {
		status = domain.ReturnAwaitingProviderRefund
	}
	result, err := scanReturnRequest(r.pool.QueryRow(ctx, `UPDATE return_requests SET status=$1, decided_by=$2, decision_note=NULLIF($3,''), decided_at=now(), updated_at=now() WHERE id=$4 AND status='vendor_confirmed' RETURNING `+returnRequestColumns, status, adminID, note, id))
	return result, err
}

func (r *ReturnRequestRepository) ConfirmByVendor(ctx context.Context, id, vendorID, userID string) (*domain.ReturnRequest, error) {
	q := `UPDATE return_requests rr SET status='vendor_confirmed', vendor_confirmed_by=$3, vendor_confirmed_at=now(), updated_at=now() FROM order_items oi JOIN vendor_orders vo ON vo.id=oi.vendor_order_id WHERE rr.id=$1 AND rr.order_item_id=oi.id AND vo.vendor_id=$2 AND rr.status='requested' RETURNING ` + returnRequestColumns
	return scanReturnRequest(r.pool.QueryRow(ctx, q, id, vendorID, userID))
}
