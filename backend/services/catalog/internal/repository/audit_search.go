package repository

// AuditSearchSQL exposes product moderation, content revisions and replays to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_user_id::text, action, 'product', product_id::text, reason, request_id, NULL::jsonb FROM product_audit_logs`
