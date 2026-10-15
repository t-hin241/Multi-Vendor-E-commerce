# Shop vacation (AF-18)

Vendor owns the schedule and the shop's `accepting_orders` flag; Catalog,
Cart and Order enforce it. Baseline: `1bd9d93` plus the AF-16/AF-18/AF-20
working-tree changes. This release adds migrations
`vendor/000015_shop_availability`, `catalog/000019_vendor_availability` and
`order/000031_vendor_availability`. Nothing here runs a production migration
or turns the feature on.

## Meaning

- A vacation is one interval `[starts_at, ends_at)` per shop (UTC, RFC3339
  with an explicit offset; a time without a zone is refused). `starts_at`
  empty = now; a start more than 5 minutes in the past is refused, at most 365
  days ahead; `ends_at` empty = until the owner resumes, otherwise after the
  start and the current time, at most 365 days after the start. Message: plain
  text, at most 500 characters.
- Moderation is untouched: the shop stays `approved` (or `suspended`). Ending
  a vacation never lifts a suspension. Only approved or suspended shops can
  schedule one.
- Each flip of `accepting_orders` bumps the shop's selling version
  (`vendors.version`) and is queued on the existing `vendor_outbox` in the
  same transaction, so it travels as `vendor.status_changed` with the new
  additive field `accepting_orders`. `availability_version` versions the
  schedule itself (`expected_version` of PUT/DELETE; 409 `version_conflict`).
- Enforcement:
  - Order checkout asks Vendor live (`Accepting`) and refuses a paused shop
    with 409 `vendor_not_accepting_orders`; the order transaction's fence
    (`LockApproved`) also checks the local `accepting_orders` and the version,
    so a checkout planned before the pause fails once Order applied it, and
    one whose fence passed before is completed (orders accepted before the
    cutover keep going; unpaid ones are not cancelled).
  - Catalog marks the shop's products unsellable in checkout snapshots,
    purchase quotes and the internal product lookup (`shop_paused=true`), and
    leaves them out of search/category listings; the shop's own page still
    lists them read-only under a vacation notice.
  - Cart shows the line as `shop_paused` (kept in the cart) and refuses new
    lines with 409 `vendor_not_accepting_orders`.
- Existing orders, returns, refunds, support and payouts are not affected.
- `enforcement_pending` (vendor console) is true until the status outbox has
  delivered the latest version to the broker. Order's live check at checkout
  already refuses during that time.
- Rollback compatibility: callers that do not send `availability=1` on
  `GET /internal/vendors/sale-status` (a Catalog/Order binary from before
  AF-18) never see an approved shop on vacation, so they treat it as not
  approved instead of selling from it. Their snapshot pages stay 100 rows.

## Deployment order

1. Back up Vendor, Catalog and Order. Apply Catalog 000019 and Order 000031
   (additive column, default true), then Vendor 000015 (additive columns,
   tables, notice types). The new shared code writes `accepting_orders`, so
   Catalog and Order must be migrated before their new images start.
2. Deploy Notification (two new notice types), Catalog, Order and Cart, then
   Vendor. Keep `FEATURE_SHOP_VACATION_ENABLED=false`.
3. Verify reconciliation: Catalog and Order `vendor_sale_status` rows have
   `accepting_orders=true` for every shop and recent `confirmed_at`.
4. Pilot: turn the flag on in staging, pause a pilot shop now and with a
   future start, try add-to-cart/checkout (409), ship an older order of the
   same shop, resume, and check the owner's notices and the admin audit
   (`entity_type=shop_availability`).
5. The scheduler runs in every Vendor replica (state in PostgreSQL, the shop
   row lock makes each transition happen once) and also with the flag off.

## Rollback

- Turn `FEATURE_SHOP_VACATION_ENABLED` off: no new schedule or change;
  cancelling, resuming and the scheduler keep working, so paused shops still
  reopen on time. Tell affected shops through the usual channel.
- Do not roll Catalog/Order back below the AF-18 image while a shop is
  paused unless you accept that older binaries hide paused shops entirely
  (see compatibility above). Vendor's down migration refuses while any
  vacation record exists; Catalog/Order down migrations must not run while the
  AF-18 images are serving.

## Operations

- Alert: `work_queue_attention_items{queue="shop_vacation_transitions_overdue"}`
  (a start or end applied more than a minute late). Check Vendor logs for
  `shop_vacation_transition_failed` and the `vendor_outbox` backlog.
- Logs: `shop_vacation_scheduled`, `shop_vacation_resumed`,
  `shop_vacation_transition` (vendor id, versions; never the message text).
- Staff need `shop.availability.write` (now grantable); the owner always has it.
- Local PostgreSQL fixture (AF-18 §12, 2026-10-14): 1,000 shops, 900 paused,
  3,000 products; 20 listing + count samples: p50 8.36 ms, p95 10.40 ms.
  Applying one projection and reading the visible listing took 0.92 ms.
- Still pending (PW-053/PW-055): real-backend E2E and staging load measurements
  across scheduler, outbox, broker and Catalog/Order. Local SQL measurements
  do not establish an end-to-end enforcement SLO.
