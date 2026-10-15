# Business reports (AF-20)

Order, Payment and Shipment each compute their own figures from their own
database; the Admin BFF only reads them side by side with the admin's token.
Baseline: `1bd9d93` plus the AF-16/AF-18/AF-20 working-tree changes. This
release adds migrations `order/000032_business_reports`,
`payment/000021_business_reports` and `shipment/000014_business_reports`.
Nothing here runs a production migration or turns the feature on.

## Definitions (`business-report-v1`)

- Period: `[from,to)` UTC RFC3339 with an offset, at most 366 days; the
  console converts inclusive Vietnam dates. Buckets (`group=day|month`) are
  Vietnam calendar days/months (`Asia/Ho_Chi_Minh`). 422 `invalid_range`.
- Each owner reads in one read-only REPEATABLE READ transaction; `as_of` is
  that snapshot's time and the drilldown cutoff. Cohorts include only rows
  created at or before `as_of`. Status-based figures (cancelled, returned,
  delivered…) are as of `as_of`; a drilldown made later shows current status
  for the same cohort ids.
- Order: `placed_orders` = parent orders whose checkout finished
  (`checkout_state=ready`) by creation time; `vendor_orders` = their shop
  sub-orders. `gmv_ordered` = merchandise subtotal snapshot before discounts,
  including unpaid and cancelled sub-orders. Rates with numerator and
  denominator: cancellation (cancelled / sub-orders), paid cancellation,
  returns (paid sub-orders with a return request not rejected / paid). A zero
  denominator gives `rate=null`. Marketplace view ranks the 20 busiest shops;
  below 20 sub-orders the rates are withheld (`insufficient_sample`).
- Payment, marketplace: `captured` = captures recorded in the period (new
  column `payment_intents.captured_at`, set when the status first becomes
  captured), `refunded` = refunds confirmed in the period
  (`resolved_at`, else last update), `net_cash = captured − refunded`. Never
  add `gmv_ordered` and `captured`.
- Payment, one shop: a capture pays for every shop of an order, so a shop's
  captured share is `unavailable` (`vendor_allocation_not_available`);
  `refunded`, `settled_sales` and `paid_out` come from the shop's settlement
  ledger by recorded time (the same entries as its statement, AF-14).
- Shipment: first delivery attempts opened in the period (opened when Order
  says the package is fulfillable: capture verified, stock committed);
  handed off, delivered, returned, lost, cancelled; late handoff = not
  cancelled and handed off (or still waiting at `as_of`) more than 72 hours
  after opening, over packages not cancelled; handoff and delivery hours
  (average, p90).

## Access

- Admin: BFF `GET /api/admin/business-reports` needs `analytics.read`; Order
  and Shipment admin routes `analytics.read`; Payment `finance.read` (without
  it the money panel shows unavailable, never zero). Each owner re-checks the
  admin role with Identity.
- Shop: `GET /api/orders/vendor/business-reports?vendor_id=`,
  `/api/shipments/vendor/business-reports?vendor_id=` need `analytics.read`;
  `/api/payments/vendor/:vendorId/business-reports` needs `finance.read`. The
  shop is checked with Vendor on every request; drilldowns are scoped the
  same way.
- Drilldowns: `.../business-reports/drilldown?metric=&cutoff=<as_of>&cursor=`
  (20 rows, at most 100), newest first, ids and counted facts only, no buyer
  data.
- Gateway rule `reports`: 120 GET/min per client IP.

## Deployment

1. Back up and apply Order 000032, Shipment 000014 (index builds lock writes
   on `vendor_orders`/`shipments` while they run: use a quiet window or build
   the same indexes `CONCURRENTLY` by hand first, the migration uses
   `IF NOT EXISTS`) and Payment 000021 (adds `captured_at`, trigger, backfill:
   history from the first captured outcome queued for Order, else the row's
   last update; flagged `captured_at_estimated`).
2. Deploy Order, Payment, Shipment, then Admin and the frontend, with
   `FEATURE_BUSINESS_REPORTS_ENABLED=false` (404 `feature_disabled`).
3. Shadow in staging: turn the flag on in all four, compare a month of
   `refunded`/`paid_out` for a pilot shop with its AF-14 statement, and the
   marketplace `captured` with provider settlement reports; check that
   aggregate = sum of drilldown at the same cutoff. If they disagree, turn the
   flag off instead of showing the figure.

## Rollback and operations

- Turn the flag off in the four services; nothing is written by reports. The
  indexes and `captured_at` can stay.
- Each owner bounds a report to 2 s (503 `report_timeout`); the BFF shows a
  slow owner as unavailable. Watch HTTP latency of `/business-reports` and the
  `admin_source_unavailable` log.
- Automated report fixtures and browser tests are recorded in AF-20 §12 (PW-054).
  Provider reconciliation and finance sign-off remain staging work (PW-056).
- Not yet implemented: daily projections/watermarks, CSV export of reports,
  charts and shop names in the ranking.
