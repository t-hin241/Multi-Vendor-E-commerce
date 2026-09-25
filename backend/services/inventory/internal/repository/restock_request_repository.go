package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/inventory/internal/domain"
)

type RestockRequestRepository struct {
	pool *pgxpool.Pool
}

func NewRestockRequestRepository(pool *pgxpool.Pool) *RestockRequestRepository {
	return &RestockRequestRepository{pool: pool}
}

var ErrRestockRequestNotFound = errors.New("repository: restock request not found")

const restockRequestSelectColumns = `
	SELECT id, inventory_item_id, product_id, variant_id, vendor_id, requested_quantity,
	       status, requested_by, rejection_reason, decided_by, decided_at, created_at, updated_at
	`

func (r *RestockRequestRepository) Create(ctx context.Context, req *domain.RestockRequest) error {
	const query = `
		INSERT INTO restock_requests (inventory_item_id, product_id, variant_id, vendor_id, requested_quantity, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, created_at, updated_at`

	return r.pool.QueryRow(ctx, query, req.InventoryItemID, req.ProductID, req.VariantID, req.VendorID, req.RequestedQuantity, req.RequestedBy).
		Scan(&req.ID, &req.Status, &req.CreatedAt, &req.UpdatedAt)
}

func (r *RestockRequestRepository) FindByID(ctx context.Context, id string) (*domain.RestockRequest, error) {
	return scanRestockRequest(r.pool.QueryRow(ctx, restockRequestSelectColumns+`FROM restock_requests WHERE id = $1`, id))
}

func (r *RestockRequestRepository) ListByStatus(ctx context.Context, status string, limit, offset int) ([]*domain.RestockRequest, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if status == "" {
		rows, err = r.pool.Query(ctx, restockRequestSelectColumns+`FROM restock_requests ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = r.pool.Query(ctx, restockRequestSelectColumns+`FROM restock_requests WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRestockRequests(rows)
}

func (r *RestockRequestRepository) ListByVendor(ctx context.Context, vendorID string, limit, offset int) ([]*domain.RestockRequest, error) {
	rows, err := r.pool.Query(ctx,
		restockRequestSelectColumns+`FROM restock_requests WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		vendorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRestockRequests(rows)
}

func (r *RestockRequestRepository) UpdateStatus(ctx context.Context, id string, status domain.RestockStatus, adminUserID string, reason *string) error {
	const query = `
		UPDATE restock_requests
		SET status = $1, rejection_reason = $2, decided_by = $3, decided_at = now(), updated_at = now()
		WHERE id = $4`
	tag, err := r.pool.Exec(ctx, query, status, reason, adminUserID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRestockRequestNotFound
	}
	return nil
}

func scanRestockRequest(row pgx.Row) (*domain.RestockRequest, error) {
	var req domain.RestockRequest
	err := row.Scan(&req.ID, &req.InventoryItemID, &req.ProductID, &req.VariantID, &req.VendorID, &req.RequestedQuantity,
		&req.Status, &req.RequestedBy, &req.RejectionReason, &req.DecidedBy, &req.DecidedAt, &req.CreatedAt, &req.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRestockRequestNotFound
		}
		return nil, err
	}
	return &req, nil
}

func scanRestockRequests(rows pgx.Rows) ([]*domain.RestockRequest, error) {
	var out []*domain.RestockRequest
	for rows.Next() {
		var req domain.RestockRequest
		err := rows.Scan(&req.ID, &req.InventoryItemID, &req.ProductID, &req.VariantID, &req.VendorID, &req.RequestedQuantity,
			&req.Status, &req.RequestedBy, &req.RejectionReason, &req.DecidedBy, &req.DecidedAt, &req.CreatedAt, &req.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, &req)
	}
	return out, rows.Err()
}
