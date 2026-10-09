package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/shopaccess"
)

// AdminAudit appends admin decisions (restock approvals and rejections) to
// inventory_operation_audit, in the caller's transaction. Rows cannot be
// updated or deleted (database trigger).
type AdminAudit struct{ Pool *pgxpool.Pool }

func (r AdminAudit) Record(ctx context.Context, entityType, entityID, actor, action string, reason *string, changes map[string]any) error {
	var encoded []byte
	if len(changes) > 0 {
		var err error
		if encoded, err = json.Marshal(changes); err != nil {
			return fmt.Errorf("encode audit changes: %w", err)
		}
	}
	_, err := connection(ctx, r.Pool).Exec(ctx, `
		INSERT INTO inventory_operation_audit (entity_type, entity_id, actor_user_id, action, reason, changes, request_id, membership_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, entityType, entityID, actor, action, reason, encoded, middleware.CorrelationID(ctx), shopaccess.UsedMembershipVersion(ctx))
	return err
}
