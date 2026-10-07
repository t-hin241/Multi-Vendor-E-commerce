-- Installed separately in each owner's database by its versioned migration.
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

-- Original timestamps only. Legacy rows NEVER emit notices until individually
-- activated with reason/audit, or entering a genuinely new stage.
WITH source AS (
 SELECT 'support'::text AS kind,c.id AS resource_id,c.created_at AS opened,
 COALESCE((SELECT max(h.created_at) FROM support_case_history h WHERE h.case_id=c.id AND h.to_status=c.status),c.created_at) AS started,
 CASE c.status WHEN 'open' THEN 'acknowledgement' WHEN 'waiting_vendor' THEN 'vendor_response' WHEN 'resolution_pending' THEN 'resolution_followup' ELSE 'operator_response' END AS stage,
 CASE c.status WHEN 'waiting_vendor' THEN 'vendor' WHEN 'waiting_buyer' THEN 'buyer' ELSE 'admin' END AS waiting,
 CASE WHEN c.status IN ('waiting_vendor','resolution_pending') THEN interval '48 hours' ELSE interval '24 hours' END AS duration,
 c.status='waiting_buyer' AS paused,c.assignee_id AS assignee,'/admin/support/'||c.id AS link
 FROM support_cases c WHERE c.status NOT IN ('resolved','closed')
 UNION ALL
 SELECT 'return',id,created_at,created_at,'return_decision','admin',interval '48 hours',false,NULL::uuid,'/admin/returns?return_id='||id
 FROM return_requests WHERE status IN ('requested','vendor_confirmed')
), shaped AS (
 SELECT *,gen_random_uuid() AS work_id,LEAST(started+duration,opened+interval '7 days') AS deadline FROM source
)
INSERT INTO case_sla_work_items(id,resource_type,resource_id,payload,active,due_at,version,created_at)
SELECT work_id,kind,resource_id,jsonb_build_object(
 'id',work_id,'resource_type',kind,'resource_id',resource_id,'stage',stage,'waiting_on',waiting,
 'sla_policy_version','case-sla-v1','stage_started_at',started,'due_at',deadline,
 'reminder_at',started+(deadline-started)*0.75,'overall_due_at',opened+interval '7 days',
 'paused_at',CASE WHEN paused THEN started ELSE NULL END,'assignee_id',assignee,
 'deadline_version',1,'version',1,'active',true,'legacy',true,'needs_attention',false,'url',link,'created_at',opened),
 true,CASE WHEN paused THEN opened+interval '7 days' ELSE deadline END,1,opened FROM shaped
ON CONFLICT(resource_type,resource_id) DO NOTHING;
