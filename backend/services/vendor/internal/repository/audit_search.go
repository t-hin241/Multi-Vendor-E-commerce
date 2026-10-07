package repository

// AuditSearchSQL exposes shop moderation, payout destination decisions, event replays, marketplace policy decisions
// and shop staff changes to the admin audit search (pkg/adminaudit): id, time, actor, action, entity, reason, request
// id and changed fields. Staff rows carry permission names only, never a token or an address.
const AuditSearchSQL = `SELECT id::text, created_at, actor_user_id::text, action, 'vendor', vendor_id::text, reason, request_id,
	CASE WHEN version IS NULL THEN NULL ELSE jsonb_build_object('version', version) END FROM vendor_audit_logs
	UNION ALL SELECT id::text, created_at, actor_user_id::text, action, 'marketplace_policy', policy_id::text, reason, request_id, NULL::jsonb
	FROM policy_audit_logs
	UNION ALL SELECT id::text, created_at, actor_user_id::text, action, 'shop_membership', vendor_id::text, reason, request_id,
	jsonb_strip_nulls(jsonb_build_object('member_user_id', member_user_id, 'invitation_id', invitation_id,
		'old_permissions', to_jsonb(old_permissions), 'new_permissions', to_jsonb(new_permissions), 'membership_version', membership_version))
	FROM membership_audit_logs`
