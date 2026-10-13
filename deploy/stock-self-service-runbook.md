# AF-13 — Vendor stock self-service

Implementation date: 2026-10-13. Default **off**; this is not a production rollout record.
Baseline: `d58d3eb` plus the AF-13 working tree. Existing Identity edits are separate.

## Deployment order

1. Back up Inventory and Notification databases. Apply Notification migration
   `000009_inventory_notices`, then deploy Notification and Vendor. Verify the
   authenticated Notification → Vendor recipient contract accepts `purpose=inventory`.
2. Apply Inventory `000010_stock_self_service`, then deploy Inventory and the
   frontend. The migration adds versions and alert metadata; it does not create
   historical movements or convert pending legacy restock requests.
3. Enable `FEATURE_VENDOR_ACTION_NOTICES_ENABLED` in Notification and verify its
   PostgreSQL/Redis/SMTP delivery path. Inventory uses its durable episode outbox
   and the existing authenticated `POST /internal/vendor-action-notices` contract,
   independently of `EVENT_PUBLISHING`. No new NATS subject or ACL is required.
4. Set Inventory's `FEATURE_STOCK_SELF_SERVICE_ENABLED=true`. Keep every shop
   policy disabled until an operator verifies that the shop keeps its own stock
   and understands that returned goods must use inspected recovery commands.
5. At `/admin/requests`, an operator with `platform.configure` selects a pilot
   vendor, reviews its policy version, records a reason and enables a limit.
   Default and maximum pilot limit: 10,000 units per positive adjustment. A lower
   per-shop limit is supported. Policy updates always advance the policy version.
6. Owner grants staff `inventory.read` and `inventory.adjust`; staff opt into the
   Inventory notification category. Owners receive required notices without opt-in.
   Vendor console `/vendor/inventory` works without Catalog permissions.

## Contracts and invariants

- Vendor API prefix: `/api/inventory/vendor`. POST `stock-adjustments` accepts
  `item_id`, `kind=receipt|damage|correction`, `quantity_delta`, `reason`,
  `evidence_ref`, `expected_version`. POST `stock-counts` accepts the same metadata
  with `physical_count` and zero/absent `quantity_delta`. A count includes reserved
  units. Evidence is a document reference, not an uploaded file or signed URL.
- PUT `items/:id/alert-settings` takes `enabled`, `low_stock_threshold`,
  `expected_version`. GET `self-service-policy?vendor_id=...` includes the server
  feature capability. GET `movements?item_id=...&cursor=...` is cursor paginated
  (default 20, maximum 100). History remains readable when self-service is off.
- Every mutation requires `Idempotency-Key`. Reuse the **same key and exact
  normalized payload** after an unknown outcome; never create a replacement key
  automatically. Different payload/actor under the same vendor adjustment key is
  409. Command receipts have no automatic expiry.
- Item writes, including legacy reserve/commit/release/restock/recovery, advance
  `version`. A stale request is 409 `version_conflict`; recount before creating a
  new request. All quantity changes, movement and cache invalidation outbox writes
  commit atomically. `on_hand = available_quantity + reserved_quantity`.
- Stock below held units produces 409 `reserved_stock_conflict` and a durable
  shortage work item; `Location` points to the operation. No holds are released.
  Admin records investigation results with a reason; resolving the work item
  itself never changes stock or the buyer's order.
- Above-limit positive adjustments return 202 `approval_required` with
  `operation_id,status_url`. GET `stock-adjustments/:id` checks shop access.
  Admin `/api/inventory/admin/stock-adjustments` lists pending approvals/shortages;
  POST `:id/decisions` takes `decision=approve|reject|resolve`, `reason`,
  `expected_version`. `moderation.manage` is required. Approval rechecks original
  actor access, shop policy version and the original item version. Changed stock
  or policy means reject and recount, never silently overwrite. Existing
  `INVENTORY_RESTOCK_SECOND_APPROVAL_QUANTITY` also applies to these approvals.
- Legacy restock/count endpoints retain their approval/decrease-only semantics.
  A legacy pending request is never automatically applied by AF-13.

## Low-stock delivery and exceptions

An available balance strictly below the configured threshold opens one episode.
Further changes below the threshold do not send another notice. Recovery to the
threshold or above rearms the next crossing. Disabling/re-enabling an alert while
still below the threshold does not create another episode.

The episode ID is Notification's deduplication key. Inventory claims a one-minute
lease, calls Notification outside the stock transaction with a two-second timeout,
and retries transient failures with bounded backoff/jitter (five attempts). Permanent
4xx refusals, except rate limiting, park immediately. Notification resolves current
owner/staff access and applies its existing inbox/email deduplication and retries.
SMTP still has at-least-once semantics when a provider times out after accepting.

`/admin/requests` shows parked alerts and offers audited retry (`platform.configure`):
GET `/api/inventory/admin/stock-alerts`, POST `stock-alerts/:id/retry` with reason and
idempotency key. Retry only after fixing Notification configuration/access/delivery.
Review Notification's own recipient/delivery queue after Inventory has handed off.

Monitor `/api/inventory/admin/operations`: `stock_approval_pending`, `stock_shortages`,
`stock_alerts_parked`, `stock_adjustments_applied_24h`, `reserved_mismatches`, and
`negative_stock` (hard gate: zero). `inventory_operations_alert` logs active shortage,
parked delivery and negative-stock problems. `stock_alert_episode` exports backlog
count/age through the existing telemetry collector. Investigate abnormal adjustment
volume; no automatic shop suspension is introduced.

## Rollback and acceptance

Disable the global Inventory flag and/or the affected vendor policy. New writes and
pending approvals stop; legacy restock requests continue through normal moderation.
Existing command receipts, movement history, holds and notification delivery remain.
Database downgrade refuses to remove AF-13 history; use forward fixes instead.

Before production acceptance: verify pilot ownership and policy approval, SMTP/inbox
delivery to a real staging account, staff revoke behavior, repeat/timeout handling,
two-admin approvals if configured, reserve/count contention, alerts across a recovery
cycle, backup restore, and operational alert routing. Local tests are not proof of
production SMTP delivery or operator readiness.

## Local verification (2026-10-13)

- Inventory, Vendor and Notification: Go 1.27.2, golangci-lint 2.14.0;
  gofmt/goimports, `go vet ./...`, lint with unlimited issue reporting,
  `go build ./...` and `go test -race -count=1 ./...` all passed after the final
  code changes. PostgreSQL integration ran for all three services; Notification
  also exercised Redis with the production queue ACL. Lint reported zero issues.
  These modules have no sqlc configuration, so generation checks do not apply.
- Frontend, Node 24: typecheck, ESLint, Prettier, 116 unit tests and production
  build passed. ESLint retains the existing `product-reviews.tsx` image warning.
  All 48 Playwright tests passed against the mocked gateway, including stock-only
  staff access, disabled policy and retrying an unknown outcome with the same key.
- Docker images for Inventory, Vendor, Notification and frontend built from the
  pinned bases; all four run as `app`. Compose configuration and `check-pins.sh`
  passed (49 pinned image references, Go toolchain 1.27.2).
- Application databases and production flags were not changed. Real-service
  browser acceptance, staging SMTP delivery and pilot rollout remain the gates
  described above; the mocked browser suite does not establish those outcomes.
