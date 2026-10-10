-- Refuses once an admin acted on a deadline (the audit is append-only);
-- before that the tables are dropped.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM case_sla_audit) OR EXISTS (SELECT 1 FROM case_sla_commands) THEN
        RAISE EXCEPTION 'case deadlines have an audit trail; turn FEATURE_CASE_SLA_ENABLED off instead';
    END IF;
END $$;
DROP TABLE case_sla_commands;
DROP TABLE case_sla_audit;
DROP FUNCTION case_sla_audit_immutable();
DROP TABLE case_sla_notice_outbox;
DROP TABLE case_sla_reminder_receipts;
DROP TABLE case_sla_work_items;
