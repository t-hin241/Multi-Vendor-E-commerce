package repository

// AuditSearchSQL exposes review moderation and shop actions to the admin
// audit search (pkg/adminaudit): id, time, actor, action, entity, note,
// request id and changed fields (with the report, if any).
const AuditSearchSQL = `SELECT id::text, created_at, actor_id::text, action, entity_type, entity_id, note, request_id,
	CASE WHEN report_id IS NULL THEN changes ELSE COALESCE(changes, '{}'::jsonb) || jsonb_build_object('report_id', report_id) END
	FROM review_moderation_audit_logs`
