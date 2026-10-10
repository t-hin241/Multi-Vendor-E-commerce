# Event contracts (PLT-03/05)

Domain events between services travel on NATS JetStream, stream `SHOPEE_EVENTS`, subject `shopee.events.<type>`. Code: `pkg/eventbus` (transport, inbox), `pkg/events` (types and payloads). `events_test.go` pins every payload.

## Envelope

| Field | Meaning |
|---|---|
| `event_id` | Stable id of the fact (outbox row / effect id). Broker dedup (10 min) and consumer inbox dedup (30 days) use it. |
| `type` | `producer.fact` |
| `schema_version` | Payload version (all `1` today) |
| `producer` | First part of `type` |
| `aggregate_id`, `aggregate_version` | Entity the fact is about; version when the producer has one (vendor, product) |
| `occurred_at` | UTC |
| `correlation_id`, `causation_id` | Request id that caused the fact, when known; otherwise the event id |
| `payload` | At most 64 KiB; references and the few fields a consumer needs |

## Events

| Type | Producer (outbox) | Consumers (durable) | Payload | Consumer semantics |
|---|---|---|---|---|
| `vendor.status_changed` | Vendor (`vendor_outbox`) | Catalog `catalog-vendor-status`, Order `order-vendor-status` | `vendor_id`, `status`, `version` | Keep highest version; older ignored |
| `vendor.notification_requested` | Vendor (`vendor_notification_outbox`) | Notification `notification-requests` | `user_id`, `type`, `reference_id` | One notification per event (dedup key) |
| `vendor.policy_published` | Vendor (`policy_outbox`) | Order `order-policy-versions` | `policy_id`, `scope` (marketplace/shop), `vendor_id`, `kind`, `version`, `content_hash`, `rule_refs`, `effective_at` | Versions are immutable: store once per `policy_id`; the active one is picked by `effective_at` at checkout |
| `catalog.product_status_changed` | Catalog (`product_status_outbox`) | Order `order-product-status` | `product_id`, `version`, `is_visible` | Keep highest version |
| `inventory.reservation_expired` | Inventory (`inventory_outbox`) | Order `order-reservation-expiry` | `id`, `order_id`, `type` | Order cancels an unpaid order whose hold expired |
| `inventory.stock_changed` | Inventory (`inventory_stock_outbox`) | Catalog `catalog-stock-cache` | `variant_ids` (1–100) | Drop cached stock; idempotent (new id per publish) |
| `order.notification_requested` | Order (effect `notify`) | Notification `notification-requests` | `user_id`, `type`, `reference_id` | As above |
| `payment.notification_requested` | Payment (`payment_buyer_notices`, PW-009) | Notification `notification-requests` | `user_id`, `type`, `reference_id` (order id) | As above |
| `order.fulfillment_ready` | Order (effect `create_shipment`) | Shipment `shipment-fulfillment-ready` | vendor order, shop, buyer, delivery address, weight, fee quote | Open the shipment once per vendor order |
| `order.fulfillment_cancelled` | Order (effect `cancel_shipment`) | Shipment `shipment-fulfillment-cancelled` | `vendor_order_id` | Stop / intercept the shipment |
| `order.vendor_order_settleable` | Order (effect `settle_vendor_order`) | Payment `payment-settlements` | checkout snapshot, commission, `completed_at`, `eligible_at` | Ledger entry once per vendor order |
| `order.payment_outcome_rejected` | Order (effect `report_rejected_outcome`) | Payment `payment-rejected-outcomes` | `kind` (payment/refund), payment or refund id, `order_id`, `reason` | Put the outcome up for review |
| `shipment.status_changed` | Shipment (`shipment_outbox`) | Order `order-shipment-facts` | `event_id`, shipment, vendor order, `type` (shipped/delivered/returned), `occurred_at`, tracking | Order decides the vendor order's state |
| `shipment.exception_detected` | Shipment (`shipment_outbox`, AF-04; only with `FEATURE_DELIVERY_RESOLUTION_ENABLED`) | Order `order-shipment-exceptions` | `event_id`, shipment, vendor order, `exception_type` (attempts_exhausted/returned/lost), `attempt_no`, `failed_attempts`, `reason` (operator note, no address), `occurred_at` | Order keeps one delivery exception per shipment (a later type updates it) and holds the payout; never refunds or restocks by itself |
| `payment.outcome` | Payment (`payment_order_sync`) | Order `order-payment-outcomes` | `payment_id`, `order_id`, `outcome` (captured/failed), `amount`, `currency`, `reason` | Order checks amount/currency; a refusal is reported back (`order.payment_outcome_rejected`) |
| `payment.refund_outcome` | Payment (`payment_refund_sync`) | Order `order-refund-outcomes` | refund ids, `status` (succeeded/failed), `amount`, `currency`, `failure_reason` | Same; a refusal is reported back |
| `order.vendor_action_required` | Order (effect `notify_vendor`, AF-08; only with `FEATURE_VENDOR_ACTION_NOTICES_ENABLED`) | Notification `notification-vendor-actions` | `vendor_id`, `vendor_order_id`, `order_id`, `action_kind` (new_order/cancellation_requested/return_requested/return_dispatched), `reference_id` | Notification records the event once, asks Vendor who receives it (`purpose` from the kind) and sends one notice per person; never changes the order |
| `payment.vendor_action_required` | Payment (`payment_vendor_notices`, AF-08; same flag) | Notification `notification-vendor-actions` | `vendor_id`, `payout_id`, `outcome` (succeeded/failed); no amount or account | As above, purpose `finance` |

Commands that need an answer stay synchronous APIs with an operation key (plan: "Inventory command cần kết quả ngay vẫn dùng API"): Order → Inventory reserve/commit/release/restock, Order → Payment refund request (Order needs Payment's refund id), Order → Shipment quote, Order ↔ Cart checkout consume, and every read.

## Delivery rules

- Producer: domain change and outbox row commit together; the relay publishes with `event_id` as `Nats-Msg-Id` and marks the row only after the broker's ACK. Broker down: rows wait and are retried with backoff (each outbox keeps its attempt limit, parking and admin replay).
- Consumer: inbox row and state change commit together, then ACK. Duplicate (`consumer`, `event_id`) does nothing. A refusal (4xx business error, malformed payload) parks at once; other failures retry 5 times with backoff 2 s, 8 s, 32 s, 128 s ±20% and then park. Parked events: `GET /api/<service>/admin/events/parked`, replay or discard with a reason (`POST /api/<service>/admin/events/:consumer/:eventId/replay|discard`), audited in `event_inbox_audit` and in the admin audit search.
- Ordering is not assumed: version checks or current state decide (older vendor/product versions are ignored; Order re-reads its own state).
- Shutdown: consumers finish the message in hand, the connection drains; nothing is ACKed before commit.

## Changing a contract

1. Additive field: add it; consumers ignore unknown fields. Update `events_test.go`.
2. Breaking change: new `schema_version`. Deploy consumers that accept both versions, then producers; remove the old version after the stream retention (14 days) and the outbox backlog are empty.
3. New event: add the type and builder here, consumers first, then the producer. The type must start with the producing service's name (`<service>.<fact>`): the broker lets a service publish only `shopee.events.<service>.>`.
4. New consumer: name its durable `<service>-<purpose>` and grant it to that service in `deploy/nats/nats.conf` (`TestBrokerPermissionsMatchTheCode` fails until it is there).

## Broker access

Every connection logs in as its service (`EVENTBUS_PASSWORD`, the service's internal key; `deploy/nats/nats.conf`). A service may publish only its own event types, use only its own durable consumers, and receive replies only on `_INBOX_<service>`; there is no anonymous access. A service holding valid credentials therefore cannot forge another service's fact (for example a `payment.outcome`) or read deliveries meant for another service.

## Rollback

`EVENT_PUBLISHING=http` makes producers call the consumers' internal HTTP routes again (kept, service-authenticated). Consumers keep reading the stream, so events already published are still applied.

## AF-07: case deadline notices

`order.work_item_reminder`, `order.work_item_overdue`, and the corresponding
`payment.*` / `shipment.*` types use schema version 1 (`events.WorkItemNotice`).
Payload: resource type/id, SLA policy version, deadline version, stage, UTC
due time, reminder kind and recipient user ID. No evidence, message text, PII,
or signed links. The owner persists one event ID per recipient in its outbox;
the receipt key is `(work_item_id,deadline_version,reminder_kind)`. Notification
accepts these through its existing `notification-requests` durable and dedup key.
They never cause money, stock or resource-lifecycle transitions. Correlation ID
is the owner work item ID. See `deploy/case-sla-runbook.md` for staged rollout.
