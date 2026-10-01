package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/domain"
)

// AuditRepository appends admin actions to shipment_admin_audit. Rows
// cannot be updated or deleted (database trigger).
type AuditRepository struct{ Pool *pgxpool.Pool }

// Record writes in the caller's transaction, so the audit and the change
// commit or fail together.
func (r AuditRepository) Record(ctx context.Context, a domain.AdminAction) error {
	var changes []byte
	if len(a.Changes) > 0 {
		var err error
		if changes, err = json.Marshal(a.Changes); err != nil {
			return fmt.Errorf("encode audit changes: %w", err)
		}
	}
	_, err := connection(ctx, r.Pool).Exec(ctx, `
		INSERT INTO shipment_admin_audit (actor_id, action, entity_type, entity_id, reason, changes, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ActorID, a.Action, a.EntityType, a.EntityID, a.Reason, changes, middleware.CorrelationID(ctx))
	return err
}

// AuditSearchSQL exposes shipment_admin_audit to the admin audit search
// (pkg/adminaudit).
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, entity_type, entity_id, reason, request_id, changes FROM shipment_admin_audit`
