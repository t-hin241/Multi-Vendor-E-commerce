package repository

// AuditSearchSQL exposes review report decisions to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, 'review', review_id::text, note, request_id,
	CASE WHEN report_id IS NULL THEN NULL ELSE jsonb_build_object('report_id', report_id) END FROM review_moderation_audit_logs`
