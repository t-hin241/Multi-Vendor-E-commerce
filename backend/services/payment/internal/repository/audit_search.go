package repository

// AuditSearchSQL exposes receipt retries, reconciliation, refunds, settlement adjustments and payouts to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, target_type, target_id, reason, request_id, NULL::jsonb FROM payment_admin_audit`
