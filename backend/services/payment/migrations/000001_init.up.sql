-- Bootstrap migration for the payment service schema: enables UUID generation
-- used by every table's primary key going forward.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
