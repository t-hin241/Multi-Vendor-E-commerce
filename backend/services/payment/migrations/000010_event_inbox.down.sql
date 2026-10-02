-- Refuses while events are parked (not applied yet) or operator actions
-- were audited: audit is never deleted.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM event_inbox WHERE status = 'parked') THEN
  RAISE EXCEPTION 'parked events would be lost';
 END IF;
 IF EXISTS (SELECT 1 FROM event_inbox_audit) THEN
  RAISE EXCEPTION 'event audit rows exist';
 END IF;
END $$;
DROP TABLE IF EXISTS event_inbox_audit;
DROP FUNCTION IF EXISTS event_inbox_audit_append_only();
DROP TABLE IF EXISTS event_inbox;
