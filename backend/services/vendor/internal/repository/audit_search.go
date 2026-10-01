package repository

// AuditSearchSQL exposes shop moderation, payout destination decisions and event replays to the admin audit search
// (pkg/adminaudit): id, time, actor, action, entity, reason, request id and
// changed fields.
const AuditSearchSQL = `SELECT id::text, created_at, actor_user_id::text, action, 'vendor', vendor_id::text, reason, request_id,
	CASE WHEN version IS NULL THEN NULL ELSE jsonb_build_object('version', version) END FROM vendor_audit_logs`
