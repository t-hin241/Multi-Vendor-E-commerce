# AF-11 — Selective cart checkout

Implementation date: 2026-10-13. Baseline: `e47cb3c555046eeb1f6ac0e54040214648065c44` plus this working-tree change. This is local implementation evidence, not a staging or production release approval.

## Contract

- `GET /api/orders/checkout/capabilities` is buyer-only and returns `selective_checkout_enabled`. It requires both Order's flag and Cart's advertised support; an old Cart never enables the UI.
- `GET /api/cart` adds `cart_id`, `selective_checkout_enabled` and per-line `vendor_id`. Selection storage is scoped by authenticated user and cart ID. Shop checkboxes expand to line IDs, never client-supplied vendor ownership.
- Preview and checkout accept `selected_line_ids`: 1–50 unique canonical UUIDs from the buyer's current cart. Omitted means legacy all-cart. Explicit `null`, `[]`, duplicates, malformed or foreign IDs are refused with 422 `invalid_selection`.
- Selective preview requires `cart_version`; selective create requires it and `expected_total_amount`. Full cart version is checked even if only an unselected line changed. Stale requests return 409 `selection_stale`.
- Only selected lines reach authoritative Catalog pricing, vendor/shipping quotes, stock reservation, policy snapshots and order items. Checkout retains price/version fencing and backend money calculations. Any selected line failure blocks the whole selection.
- `POST /internal/carts/:buyerId/checkout-snapshots` keeps its existing path and adds `selected_line_ids`. The receipt contains only selected line snapshots, their original quantities, `cart_version` and `selection_hash`. The old proposed singular `/internal/cart/...` path is not introduced.
- Selective consume must match the complete stored receipt. Cart derives the quantities from that immutable receipt, preserves later quantity increases and all other line instances, and replays a previous consume without applying twice. Existing legacy receipts keep their old semantics.
- Checkout request hashing includes sorted selection IDs plus the existing address, cart version, expected total, quote version and accepted policies. Same key with changed input conflicts; completed requests replay before feature gates or any live cart read, including after consume and flag rollback. The existing key retention window remains 24 hours.

## Concurrency and recovery

V1 serializes checkout per buyer, including disjoint selections. The pre-existing preparing-operation and consumption workflow is strengthened with a PostgreSQL transaction advisory lock in Order's `Begin`: a second key cannot start while another operation is preparing or consumption is held/pending/parked. The lock is released before network calls; the durable operation protects the remainder of the saga. New competing keys receive 409 `line_claimed`; retrying an existing key retains its existing outcome.

Cart owns row locking, snapshot insertion and consume transactions. Order never accesses Cart's database. This version uses the serialized coordinator option instead of independent active line-claim rows. Cart's preview version and lines are read under one lock. Order's existing worker recovers preparing operations, reserves/releases inventory and retries consumption. A parked task keeps new checkouts blocked for operator investigation; it is not an excuse to drop unpurchased items or retry under a new key.

The order/cart-consumption transaction and its fence on the preparing operation are unchanged. A failed checkout before order creation does not hold a permanent cart lock. A successful order survives a Cart outage; consume uses its durable task and frozen quantities. Cancellation/payment failure after consume does not reinsert cart lines.

## Rollout

1. Keep `FEATURE_SELECTIVE_CHECKOUT_ENABLED=false`. Check Cart/Order are on compatible code and drain checkout traffic while replacing all Order instances; old instances do not participate in the new per-buyer admission lock.
2. Apply Cart migration `000006_selective_checkout`. It adds `selection_hash` and a trigger protecting receipt identity/lines/version. Legacy rows use an empty selection hash and retain their original stored snapshots; nothing is rebuilt from today's cart. Order needs no new schema for selection: the existing request hash and consumption JSON carry it.
3. Deploy Cart first, then Order, with workers running. Deploy frontend after consumers. Verify legacy all-cart checkout and duplicate-key replay while flags remain off.
4. Enable Cart's flag, then Order's flag. Compose exposes the same variable to both services; recreate Cart first, then Order. Verify buyer capability is true. Client code has no independent enable flag.
5. In staging, exercise two shops, selected SKU prices, unselected out-of-stock lines, selected shipping refusal, address changes, concurrent tabs, lost responses and worker restart. Inspect order totals/reservations and the exact remaining cart lines before enabling real buyers.

## Rollback and operations

Disable Order's flag, then Cart's. New selection UI disappears on capability refresh; new selected creates are refused. Keep the new readers, receipt schema and workers deployed so existing operations and consumption finish. Migration down refuses while selective receipts exist. Do not roll back to an old Cart binary that could ignore selection or delete/rebuild receipts to make down succeed.

Watch `checkout_selection_refusals_total{code}` for `selection_stale`, `line_claimed`, `invalid_selection`, `checkout_total_changed`, `selective_checkout_disabled`. Labels contain no buyer IDs or line IDs. `work_queue_attention_items{queue="cart_consumptions_stuck"}` and its age metric report parked tasks or held/pending tasks older than five minutes; the existing critical work-queue alert applies. Existing `order_cart_consume_*` worker logs retain order/operation references for reconciliation.

Read-only checks in the owning databases:

```sql
-- Cart: selected operations awaiting consume; includes abandoned pre-order snapshots.
SELECT count(*) FROM cart_checkout_operations
WHERE selection_hash <> '' AND consumed_at IS NULL;

-- Order: the authoritative backlog of orders whose cart cleanup is unfinished.
SELECT status, count(*), min(created_at)
FROM cart_consumptions
WHERE status IN ('held', 'pending', 'parked') GROUP BY status;

SELECT count(*) FROM checkout_operations WHERE status = 'preparing';
```

## Local checks

`deploy/selective-checkout-test.compose.yml` starts an isolated PostgreSQL 16 database on loopback port 55439 using disposable tmpfs data. Use it only for tests. Both test environment variables may target `postgres://selective_test@127.0.0.1:55439/selective_test?sslmode=disable`; each repository test creates its own schema. No credentials or application database are involved.

From `backend`, run `go test -count=1 ./services/cart/... ./services/order/... ./pkg/cartselection/...` with `CART_TEST_DATABASE_URL` and `ORDER_TEST_DATABASE_URL` set, then `go vet` for the same packages. Tests cover legacy migration round trips, immutable selected receipts, consume retry, later quantity increases, foreign/duplicate lines, global version conflicts, concurrent keys, blocked backlog, canonical replay and Cart outages.

From `frontend`, run `npm test`, `npm run typecheck`, `npm run lint`, then `npm run test:e2e -- selective-checkout.spec.ts checkout.spec.ts --project=desktop`. Browser tests use the mock gateway; PostgreSQL tests exercise real repository transactions. Full-stack staging tests and provider sandbox verification remain release gates, not claims made by mock browser tests.

Verified locally on 2026-10-13: Cart/Order/shared selection Go tests and PostgreSQL integration, `go vet`, clean Go formatting; 116 frontend tests; TypeScript; ESLint with no errors (one pre-existing `product-reviews.tsx` image warning); Prettier for changed files; production build and seven Playwright checkout tests, including late preview responses. The disposable test database/container was removed after verification. Application databases and feature flags were not changed.
