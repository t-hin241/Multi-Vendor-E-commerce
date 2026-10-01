package repository

// AuditSearchSQL exposes restock decisions and reservation repairs to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_user_id::text, action, entity_type, COALESCE(entity_id, order_id::text), reason, request_id, changes FROM inventory_operation_audit`
