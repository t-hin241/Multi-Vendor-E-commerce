-- Refuse to drop recorded refund destinations or transfer attempts: they
-- are financial evidence. Roll back with the feature flag instead.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM refund_destinations) OR EXISTS (SELECT 1 FROM manual_refund_attempts) THEN
        RAISE EXCEPTION 'manual refund data exists; turn FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED off instead of migrating down';
    END IF;
END $$;

DROP TABLE refund_evidence;
DROP FUNCTION refund_evidence_guard();
DROP TABLE manual_refund_attempts;
DROP TABLE refund_destinations;
DROP FUNCTION manual_refund_guard();
