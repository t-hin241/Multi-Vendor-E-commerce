package repository

// AuditSearchSQL exposes account locks, unlocks and session revocations to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, 'user', user_id::text, reason, request_id, NULL::jsonb FROM identity_audit_logs`
