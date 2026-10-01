package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
)

// AuditRepository appends admin actions to order_admin_audit. Rows cannot
// be updated or deleted (database trigger).
type AuditRepository struct{ pool *pgxpool.Pool }

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

// Record writes in the caller's transaction, so a failed audit write fails
// the action and a failed action leaves no audit row.
func (r *AuditRepository) Record(ctx context.Context, a domain.AdminAction) error {
	var changes []byte
	if len(a.Changes) > 0 {
		var err error
		if changes, err = json.Marshal(a.Changes); err != nil {
			return fmt.Errorf("encode audit changes: %w", err)
		}
	}
	_, err := connection(ctx, r.pool).Exec(ctx, `
		INSERT INTO order_admin_audit (actor_id, action, entity_type, entity_id, order_id, reason, changes, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ActorID, a.Action, a.EntityType, a.EntityID, a.OrderID, a.Reason, changes, middleware.CorrelationID(ctx))
	return err
}

// AuditSearchSQL exposes order_admin_audit to the admin audit search
// (pkg/adminaudit).
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, entity_type, entity_id, reason, request_id, changes FROM order_admin_audit`
