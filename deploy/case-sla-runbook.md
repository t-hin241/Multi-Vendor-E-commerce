# Case response deadlines (AF-07)

Owner databases retain deadlines even when notices are disabled. No timeout in
this feature cancels an order, approves a return, refunds money, releases stock,
or changes a payment-link expiry. Admin only reads the owner APIs.

## Current stage coverage

| Owner | Resource/stage | Response window |
|---|---|---|
| Order | Support acknowledgement / operator response | 24 hours |
| Order | Support waiting for vendor / resolution follow-up | 48 hours |
| Order | Return requested or vendor-confirmed, awaiting decision | 48 hours from request; confirmation does not reset it |
| Shipment | Interception requested | 4 hours from interception request |
| Payment | Refund awaiting a receipt, manual workflow off (`awaiting_refund_receipt`) | 72 hours from refund request |
| Payment | Manual workflow (AF-06): submitted account awaiting verification (`refund_destination_verification`) | 24 hours from submission |
| Payment | Manual workflow: verified account, transfer not submitted (`manual_refund_ready`; claiming keeps it) | 72 hours from verification |

All deadlines are UTC elapsed time. The original case has an outer seven-day
limit. Waiting for a buyer pauses the operator's remaining budget, but not the
outer limit. Repeated messages, assignment and vendor confirmation do not renew
the budget. A new stage snapshots `case-sla-v1`; reopen advances the deadline
version and keeps the original outer deadline and breach history.

AF-03 cancellations, AF-04 delivery exceptions and AF-05 return shipping have
their own stages (see the owners' runbooks). Under the manual refund workflow
(PW-017) a refund has no admin deadline while the buyer has not given an
account (the buyer is reminded instead), and none once a transfer is
`submitted` or `unknown` (the manual refund review queues follow those). The
outer limit of a manual refund counts from the first submitted account. Open
refunds still on `awaiting_refund_receipt` when the workflow is turned on are
moved to their manual stage by Payment's minute worker. Since PW-009 a stage
waiting on the shop (support reply, failed-delivery goods receipt) also tells
the shop at its reminder and when overdue (`vendor_action_required`), once per
deadline version; escalations stay with admins. Admin notices go to the
responsible, on-call and escalation admins.

## Rollout

1. Back up the owner DBs; apply Order 000018, Payment 000011, Shipment 000008
   before starting the new binaries. Migrations only touch their own database.
   They backfill open cases from original timestamps, with `legacy=true`.
2. Keep `FEATURE_CASE_SLA_ENABLED=false` in each owner. Compose uses
   `ORDER_FEATURE_CASE_SLA_ENABLED`, `PAYMENT_FEATURE_CASE_SLA_ENABLED`, and
   `SHIPMENT_FEATURE_CASE_SLA_ENABLED` to set that variable separately.
   `SLA_POLL_INTERVAL=60s`; each poll handles at most 100 items.
3. Inspect `/admin/work-items` (including the legacy filter), and the structured
   `case_sla_report`: unassigned, overdue, legacy, extension count, parked notices,
   and reminder lag. Technical workflow backlog metrics remain separate.
4. Set `SLA_ON_CALL_ADMIN_IDS` and `SLA_ESCALATION_ADMIN_IDS` to comma-separated
   real active admin IDs through deployment configuration. Startup rejects
   sending without both lists. Assignment targets and recipients are checked
   with Identity. A revoked assignment becomes unassigned; an unavailable
   Identity service causes retry, not silent loss.
5. Accept the policy windows and recipient roster in the release record, then
   enable one owner at a time. Existing legacy rows remain silent. After reviewing
   a specific row, use “Cho phép nhắc hồ sơ này” with a reason; this records an
   audited activation. A genuinely new stage also leaves legacy mode. There is
   deliberately no unreviewed bulk activation.

## APIs and recovery

- Owner GET `/api/{orders|payments|shipments}/admin/work-items` accepts `status`
  (`active`, `overdue`, `unassigned`, `needs_attention`, `legacy`), `assignee_id`,
  `limit` (20 by default, at most 100), and opaque `cursor`.
- POST `/:workItemID/assignments`, `/extensions`, `/activations`, and
  `/notice-replays` stay in the owner. All need `Idempotency-Key`,
  `expected_version` and `reason`; assignment adds `assignee_id` (empty clears it),
  extension adds RFC3339 `new_due_at`. A changed stage/version returns
  `409 stage_changed`; invalid extensions return `409 invalid_extension`.
  Extensions cannot pass the original seven-day cap or erase an earlier breach.
- GET `/api/admin/work-items` forwards the signed-in admin credentials to fixed
  owner routes. It returns independent pages and source availability, accepting
  `order_cursor`, `payment_cursor`, `shipment_cursor`. A failed/unconfigured
  source has `page:null`, never a zero count. The screen displays this explicitly.
- Every mutation is audited transactionally and appears in `/admin/audit` as
  `sla_*`, including before/after deadlines, actor, reason and request ID.
- A reminder at 75%, overdue, and three daily escalation rounds each have a
  unique receipt per work item/deadline version/kind. Restart catches up overdue
  rounds; it does not send an obsolete pre-deadline reminder. After the third
  escalation the item needs attention and stays open. Receipt and notice outbox
  commit together. Publishing occurs outside the transaction with a lease,
  five attempts and bounded exponential backoff plus jitter. Notification
  deduplicates the stable event ID and uses its existing durable delivery queue.
- Fix the cause of `parked` notices, then POST `/notice-replays` for the work item
  with its current version, a fresh key and an explanation. This resets only
  that item's parked, undelivered notices; replay retains event IDs.
- `case_sla_clock_skew` means app and DB time differ by more than 30 seconds;
  the poll stops sending until the clocks agree. Investigate time synchronization.
- Notices carry full resource references and the exact domain screen path.
  UI details still require the recipient's current role. Support buyer/vendor
  views and return/shipment DTOs expose only `action_due_at` and `waiting_on`
  from the SLA record, not the internal receipt or escalation state.

Rollback: disable the owner flag. This stops producing and relaying SLA notices,
while deadlines, audit and receipts remain. Already accepted notifications can
still be delivered; pause Notification delivery separately if required. Do not
down-migrate to erase SLA history. The down migrations intentionally retain it.

## Verification

Use a disposable PostgreSQL database ending in `_test`. Set
`CASE_SLA_TEST_DATABASE_URL`, `ORDER_TEST_DATABASE_URL`,
`PAYMENT_TEST_DATABASE_URL`, `SHIPMENT_TEST_DATABASE_URL` through the test
environment; tests create isolated schemas. Run Go tests for `pkg/casesla`,
`pkg/config`, `pkg/events` and the affected services. Frontend checks:
`tsc --noEmit --incremental false`, Vitest SLA/support suites, ESLint on changed
components. Do not enable delivery against real cases as a test.
