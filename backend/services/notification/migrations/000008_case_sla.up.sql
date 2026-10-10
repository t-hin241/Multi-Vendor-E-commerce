-- PW-045: Notification owns AF-07 deadlines for shop notices nobody could
-- receive (no_recipient) or Vendor never answered (parked); the admin
-- follows them up in /admin/work-items. Same tables as the other owners.
CREATE TABLE case_sla_work_items (
    id uuid PRIMARY KEY,
    resource_type text NOT NULL,
    resource_id uuid NOT NULL,
    payload jsonb NOT NULL,
    active boolean NOT NULL,
    due_at timestamptz NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    next_check_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(resource_type, resource_id)
);
CREATE INDEX case_sla_active_due ON case_sla_work_items(due_at,id) WHERE active;
CREATE INDEX case_sla_scan ON case_sla_work_items(next_check_at,id) WHERE active;
CREATE INDEX case_sla_page ON case_sla_work_items(created_at DESC,id DESC) WHERE active;
CREATE TABLE case_sla_reminder_receipts (
    work_item_id uuid NOT NULL REFERENCES case_sla_work_items(id),
    deadline_version bigint NOT NULL,
    reminder_kind text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(work_item_id,deadline_version,reminder_kind)
);
CREATE TABLE case_sla_notice_outbox (
    id uuid PRIMARY KEY,
    work_item_id uuid NOT NULL REFERENCES case_sla_work_items(id),
    envelope jsonb NOT NULL,
    attempts int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    delivered_at timestamptz,
    parked boolean NOT NULL DEFAULT false
);
CREATE INDEX case_sla_pending_notice ON case_sla_notice_outbox(next_attempt_at) WHERE delivered_at IS NULL AND NOT parked;
CREATE TABLE case_sla_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_item_id uuid NOT NULL REFERENCES case_sla_work_items(id),
    actor_id text NOT NULL,
    action text NOT NULL,
    reason text NOT NULL,
    request_id text,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE FUNCTION case_sla_audit_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'SLA audit is append-only'; END;
$$;
CREATE TRIGGER case_sla_audit_immutable BEFORE UPDATE OR DELETE ON case_sla_audit
FOR EACH ROW EXECUTE FUNCTION case_sla_audit_immutable();
CREATE TABLE case_sla_commands (
    actor_id uuid NOT NULL,
    work_item_id uuid NOT NULL REFERENCES case_sla_work_items(id),
    action text NOT NULL,
    key text NOT NULL,
    request jsonb NOT NULL,
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(actor_id,work_item_id,action,key)
);

-- Events already waiting for review enter the queue as legacy items (no
-- notices until activated), with their original timestamps.
INSERT INTO case_sla_work_items(id,resource_type,resource_id,payload,active,due_at,version,created_at)
SELECT w.id,'vendor_action',e.id,jsonb_build_object(
 'id',w.id,'resource_type','vendor_action','resource_id',e.id,'stage','vendor_action_'||e.status,'waiting_on','admin',
 'sla_policy_version','case-sla-v1','stage_started_at',e.updated_at,'due_at',LEAST(e.updated_at+interval '24 hours',e.created_at+interval '7 days'),
 'reminder_at',e.updated_at+interval '18 hours','overall_due_at',e.created_at+interval '7 days','paused_at',NULL,'assignee_id',NULL,
 'deadline_version',1,'version',1,'active',true,'legacy',true,'needs_attention',false,
 'url','/admin/notifications','created_at',e.created_at),
 true,LEAST(e.updated_at+interval '24 hours',e.created_at+interval '7 days'),1,e.created_at
FROM vendor_action_events e CROSS JOIN LATERAL (SELECT gen_random_uuid() AS id) w
WHERE e.status IN ('no_recipient','parked')
ON CONFLICT(resource_type,resource_id) DO NOTHING;
