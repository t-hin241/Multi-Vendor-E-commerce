# Vendor statements (AF-14)

Payment owns the statement and export records. Baseline: `af1edef` plus the
AF-14 working-tree changes. This release adds migration
`payment/000020_vendor_statements`; it does not run a production migration or
enable the feature automatically.

## Meaning of the numbers

- Periods are `[from,to)` UTC RFC3339 timestamps. The console converts inclusive
  Vietnam calendar dates to that interval. V1 accepts VND only, at most 366 days.
- `opening + credit - debit = closing`, from signed immutable settlement entries
  filtered by `created_at` (exposed as `recorded_at`). Captured payments and Order
  status are not a substitute for this ledger.
- `held`, `not_eligible`, `payable`, `in_payout` describe the current snapshot at
  `as_of`, independently of the selected historical period. Refunds and negative
  adjustments reduce payable immediately, matching the payout selection rule.
  Negative balances carry forward; they are not clamped to zero.
- Local active holds, unresolved refunds and payout claims are read in the same
  read-only REPEATABLE READ transaction as totals and entries. Until the existing
  hold-backfill gate permits `SETTLEMENT_ORDER_HOLD_QUERY=off`, the compatibility
  Order hold check also remains active; an unavailable source fails with 503.
- `watermark` is the latest ledger recording visible to the query, not a promise
  that all upstream events have arrived. Each page is a new consistent response;
  use an export for a fixed multi-page snapshot.
- New entries record actual known occurrence facts separately. Historical rows
  without that fact show recorded time and `occurred_at_estimated=true`; no money
  or currency is backfilled. Legacy succeeded payouts without currency increase
  `gap_count`, set `balances_available=false` and block export with 503.

## Configuration and deployment

1. Back up Payment and apply migration 000020 before the new image receives
   traffic. The migration is additive and preserves the ledger's existing
   append-only trigger. Do not run down after export records exist.
2. Keep `FEATURE_VENDOR_STATEMENTS_ENABLED=false` until finance has approved a
   representative sample (partial refunds before/after payout, held orders,
   negative carryover, old payout gaps) against the ledger and CSV.
3. Configure `EXPORT_RETENTION_HOURS` (24 by default, 1–168 allowed).
4. Provision a **private** S3-compatible bucket and scoped runtime credentials.
   Set `STATEMENT_STORAGE_ENDPOINT`, `STATEMENT_STORAGE_BUCKET`,
   `STATEMENT_STORAGE_USE_SSL`, `STATEMENT_STORAGE_ACCESS_KEY` and
   `STATEMENT_STORAGE_SECRET_KEY` through the deployment secret mechanism.
   Do not copy credentials into committed files. The private adapter refuses a
   bucket carrying a bucket policy; never point it at the public media bucket.
5. If the storage network endpoint differs from the browser origin, set
   `STATEMENT_STORAGE_DOWNLOAD_ORIGIN` to the externally reachable origin.
   Production downloads require HTTPS. The signing adapter uses the existing
   S3-compatible default region `us-east-1` for an alternate download origin;
   verify the deployment's storage region and proxy host handling before rollout.
   Requests must retain the signed host and path. This origin must resolve from
   the browser; an internal Compose hostname is not a usable download origin.
6. Verify Vendor `finance.read` and Identity admin `finance.read` are provisioned.
   Buyer accounts with shop membership may read statements; no global vendor
   role is granted. The existing AF-17/AF-19 feature policies still apply.
7. Enable Payment's flag, then verify `/vendor/statements` and the admin Payment
   drilldown. The UI reads the server capability and never enables the backend.

## Contracts

All paths below are rooted at `/api/payments/vendor/:vendorId`:

- GET `/statement-capabilities` returns `statements_enabled` after authorization.
- GET `/statements?from=...&to=...&currency=VND&limit=20&cursor=...` returns balances,
  entries, `next_cursor`, `as_of`, `watermark` and `gap_count`. Limit is 1–100.
- GET `/payouts/:id` returns the amount, currency, status, batch id, masked
  destination and ledger entry references, never full beneficiary details.
- POST `/statement-exports` requires `Idempotency-Key` and
  `{from,to,currency:"VND",format:"csv"}`. It returns 202 with `operation_id`,
  `status_url`, status, expiry and the snapshot summary. Over 100,000 rows returns
  422 and asks the caller to split the period.
- GET `/statement-exports/:id` authorizes the shop again.
- POST `/statement-exports/:id/download-links` authorizes again, then returns a
  private download URL valid for at most 60 seconds and no later than expiry.

Admin GET equivalents for capabilities, statements and payouts are rooted at
`/api/payments/admin/vendor/:vendorId`; both router and use case require finance
read permission. There are no new Admin-service mutations. Corrections continue
through Payment's existing audited approval workflow.

An export key is unique within the vendor. Replay by the same requester with the
same normalized period returns the same operation and cutoff. Another requester
or changed period conflicts. The immutable materialized rows are captured with
the read response before queuing: later commits (even with earlier recording
timestamps) cannot enter the CSV. A new key intentionally creates a new snapshot.
Save the returned operation id to reopen the file in the console.

Concurrent duplicate inserts read the winning operation on the same transaction
connection. This avoids waiting for an additional pool connection while all
connections are held by duplicate requests. The regression test
`TestStatementDuplicateExportWithSingleConnection` exercises this replay path
with a one-connection pool; the golden test also submits exports concurrently.

## Worker, recovery and rollback

- PostgreSQL leases serialize worker claims, with five attempts maximum and
  bounded backoff/jitter. The worker rechecks the requester's current finance
  permission, then streams saved rows into a private object. It stores SHA-256;
  the checksum is sealed in PostgreSQL before uploading, so retries under a
  changed encoder cannot overwrite the operation's object with different bytes.
  CSV text formula prefixes are neutralized; numeric signed amounts stay numeric.
- `pending/processing → ready` or `failed`; expired objects and materialized rows
  are removed idempotently. Operation metadata remains for key replay and audit.
  A failed operation can be investigated and resubmitted under a new key after
  restoring permissions/storage; never alter its snapshot.
- Monitor `payment_statement_exports` through the existing outbox count/oldest-age
  metrics, and `statement_exports_failed` / `statement_reconciliation_delta`
  through operator queue metrics. Logs use
  operation ids, never signed links, beneficiary numbers or CSV contents.
- Reconcile the CSV amount sum plus its opening against its closing and the
  saved API snapshot. Any nonzero delta blocks rollout. The automated golden
  dataset asserts those equalities; finance must additionally sign the sample.
- Roll back by disabling the flag: no new statements/exports, accepted jobs
  continue, unexpired ready files remain downloadable after authorization.
  Keep storage configured until retention cleanup finishes. Do not delete
  ledger rows, historical payouts or the migration to roll back the UI.

## Local verification (2026-10-14)

Validated against baseline `af1edef` plus the AF-14 working-tree changes:

- Shared Go package and Payment module: Go 1.27.2, golangci-lint 2.14.0,
  gofmt/goimports, vet, lint, build and full race tests passed. Integration tests
  used isolated PostgreSQL and MinIO, including immutable export snapshots,
  ledger/API/CSV reconciliation and anonymous-denied/signed object downloads.
- Frontend: typecheck, lint, formatting and all 118 unit tests passed; all 49
  Playwright tests passed. Lint retains the existing `product-reviews.tsx`
  image-element warning, with no errors.
- Frontend production build and Payment/frontend container builds passed;
  both runtime images use user `app`. Compose configuration and diff whitespace
  checks passed. No production migration or deployment was performed.
- Follow-up CI concurrency review: fixed export replay acquiring a second pool
  connection while its transaction held the first. The new one-connection
  regression failed with a timeout before the fix and passed afterwards.
  Shipment/Payment replay tests passed 20 consecutive runs; the exact combined
  eventbus/events, Inventory, Cart, Order, Payment, Shipment and Notification
  integration command passed with PostgreSQL, Redis and NATS. Payment's full
  format/vet/lint/build/race checks and container build passed again after this fix.

## Acceptance still required before production

Record finance sample sign-off, private bucket/proxy HTTPS validation, realistic
100,000-row performance, retention cleanup evidence and operator ownership in the
release record. Local automated tests do not constitute production approval.
