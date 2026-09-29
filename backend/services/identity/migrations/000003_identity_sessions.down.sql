-- Remove the session-family column, reset delivery storage and identity audit records.
DROP TABLE identity_audit_logs;
DROP INDEX users_normalized_email_key;
DROP TABLE password_reset_deliveries;
DROP INDEX password_reset_expiry_idx;
DROP INDEX refresh_tokens_family_idx;
ALTER TABLE refresh_tokens DROP COLUMN family_id;
