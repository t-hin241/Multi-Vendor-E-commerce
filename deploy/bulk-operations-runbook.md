# AF-16 — Bulk product and stock operations (CSV)

Implementation date: 2026-10-14. Default **off** in both owners; this is not a production rollout record.
Baseline: `1bd9d93` plus the AF-16 working tree. Spec: `docs/modular/add_features/16-bulk-product-stock-operations.md`.

## What exists

| Template (v1) | Owner | Rows do | Permission (read / write) |
|---|---|---|---|
| `products` | Catalog | create **draft** products (`client_row_id`, `category_id`, `name`, plain-text `description`, `price_amount`, `attr.<code>` columns) | `products.read` / `products.write` |
| `prices` | Catalog | change a SKU price, or the price of a product without SKUs (`sku` or `product_id`, `price_amount`, optional `expected_version`) | `products.read` / `products.write` |
| `stock_receipts` | Inventory | add received units through the AF-13 `receipt` adjustment (`sku` or `product_id`, `quantity`, `reason`, `evidence_ref`) | `inventory.read` / `inventory.adjust` |

Exports: `prices` and `stock_receipts` (template format plus read-only reference columns), at most 50,000 rows.
Not in v1: editing content of existing products by file, absolute stock counts, returns, orders/payments.

## Deployment order

1. Back up the Catalog and Inventory databases. Apply Catalog `000018_bulk_operations`
   and Inventory `000011_bulk_operations` (additive: job tables, row table, export
   table, append-only job events, Catalog `product_import_refs`).
2. Create a **private** bucket per owner (defaults `catalog-imports`, `inventory-imports`)
   with no bucket policy; the services refuse a bucket that has one. Configure
   `IMPORT_STORAGE_ENDPOINT`, `IMPORT_STORAGE_ACCESS_KEY`, `IMPORT_STORAGE_SECRET_KEY`
   (secrets via environment only) and `IMPORT_STORAGE_DOWNLOAD_ORIGIN` — the browser-reachable
   HTTPS origin used only for 60-second signed downloads. In production an enabled flag
   without storage, or a plain-HTTP download origin, stops the service at start.
3. Deploy Catalog (it also serves `POST /internal/catalog/vendor-targets`, the shop-scoped
   SKU/product lookup Inventory needs), then Inventory, then the frontend, all with the
   flags off. The gateway already accepts multipart up to 22 MB; Inventory lifts its 1 MiB
   JSON cap only for `POST /api/inventory/vendor/import-jobs`.
4. Enable `FEATURE_BULK_OPERATIONS_ENABLED` in Catalog (compose: `CATALOG_BULK_OPERATIONS_ENABLED`)
   for a pilot. `stock_receipts` additionally needs Inventory's flag
   (`INVENTORY_BULK_OPERATIONS_ENABLED`) **and** AF-13 enabled for the shop; otherwise its
   template reports why it is disabled and uploads are refused.
5. Limits: `IMPORT_MAX_ROWS` (1–1000, default 1000), `IMPORT_MAX_BYTES` (≤ 5,242,880).

## Contracts and invariants

- Vendor routes (both owners, prefix `/api/{catalog|inventory}/vendor`):
  `GET import-templates/:kind?vendor_id=`, `GET import-jobs?vendor_id=&cursor=`,
  `POST import-jobs` (multipart `vendor_id`, `kind`, `template_version`, `file`; header
  `Idempotency-Key`) → 202, `GET import-jobs/:id`, `GET import-jobs/:id/rows?status=&cursor=`,
  `POST import-jobs/:id/confirm {preview_hash, expected_version, acknowledge_duplicate_file}`,
  `POST import-jobs/:id/cancellations`, `POST import-jobs/:id/report-downloads`,
  `POST export-jobs {vendor_id, kind}` (+`Idempotency-Key`) → 202, `GET export-jobs/:id`,
  `POST export-jobs/:id/download-links`. Admin: `GET /api/{owner}/admin/import-jobs`,
  `POST /api/{owner}/admin/import-jobs/:id/cancellations {reason}` (`moderation.manage`).
  Deviation from the spec draft: the upload creates the job directly (no separate `file_id`).
- The shop comes from the job, never from the client; every call re-checks the shop
  permission with Vendor. Jobs of other shops are 404.
- File rules (422 `file_invalid`, no job created): UTF-8 (BOM allowed), known header
  columns only, required columns present, equal column count, ≤ max rows/bytes. Row rules
  (row `invalid`, file kept): no cell starting with `= + - @`, no HTML, links or media URLs,
  money/quantities are whole numbers (no decimals, separators, signs), SKU/product of the
  shop only (a foreign SKU looks missing), one row per target (`duplicate_target` on every
  occurrence).
- Dry-run changes nothing and holds nothing. Rows record old/new values, the planned input,
  the owner version seen (`expected_version`) and an immutable `operation_ref`
  (`import:<job>:<row>`). `preview_hash` pins what was shown; confirm needs the same hash and
  job version, a preview younger than 24 h, and an explicit acknowledgement when the same
  file (content hash + kind) was already applied by another job (409 `duplicate_file`).
- Apply: two in-process workers, one row per transaction in the owner's database. The owner
  use case (price edit, product create, AF-13 receipt) and the row result commit together, so
  a crash can never apply a row twice; the row's `operation_ref` is also the request id of
  the Catalog price audit and the AF-13 idempotency key. A changed version fails the row with
  `preview_stale` (no last-write-wins). Receipts above the shop limit become
  `needs_approval` (AF-13 approval queue). 403 (permission revoked, feature disabled) fails
  the row and stops the remaining rows (`permission_revoked`); applied rows stay. Transient
  errors retry with backoff up to 5 attempts, then `apply_unavailable`.
- A price change returns an approved product to review, as in the editor. Created products
  are drafts. `client_row_id` creates at most one product per shop, across jobs.
- Cancel stops rows not yet applied; nothing applied is undone. Summary always satisfies
  `total = invalid + unchanged + pending + succeeded + needs_approval + failed + cancelled`.
- Retention: original files 7 days after processing, error reports and exports 24 h
  (deleted by the worker). Job rows, summaries and `import_job_events` (append-only:
  uploaded, confirmed, cancelled, admin_cancelled, stopped, report/export downloads) stay.

## Rollback

- Turn the owner flag off: new uploads, confirms and exports are refused; confirmed jobs
  drain. To stop them, an operator cancels with a reason at `/admin/requests`
  ("Nhập hàng loạt của các shop"); the event is audited. Never roll back by deleting
  created products or reversing movements; fix data with ordinary edits/adjustments.
- The down migrations refuse once any job has been confirmed; keep the tables and the
  flag off instead.

## Monitoring

- Metric backlog `import_rows` (rows of confirmed jobs still to apply, oldest age) in both
  owners. Logs: `import_row_retry`, `import_row_parked`, `import_validation_attempt_failed`,
  `import_export_attempt_failed`, `import_orphan_object`. Watch `apply_unavailable` and
  `permission_revoked` counts per day and storage errors (503 `bulk_operations_unavailable`).

## Acceptance still required (staging)

- Real MinIO/S3 private bucket: upload, signed download expiry (60 s), retention cleanup.
- Pilot shop with Unicode names and variants; a malformed file, a BOM/Excel export, decimals
  and overflow, cross-shop SKU, duplicate rows, permission revoked mid-job, worker restart
  mid-job, cancel partway, stale price, and the same file uploaded with another intent.
- Check each applied row produced exactly one price audit / one movement, and
  `succeeded + failed + pending + … = rows`.

## Local verification (2026-10-14, after the last code change)

- Go 1.27.2, golangci-lint 2.14.0, modules `pkg`, `services/catalog`, `services/inventory`:
  gofmt and goimports clean, `go vet`, lint (0 issues), `go build` and `go test -race`
  pass (Linux container; Catalog/Inventory integration against a disposable PostgreSQL 16.15).
- Frontend: typecheck, ESLint (only the existing `<img>` warning in `product-reviews.tsx`),
  Prettier on changed files, 125 unit tests, production build and 51 Playwright tests
  (mocked gateway) pass. Prettier reports 7 committed AF-14 files only because of CRLF in
  the Windows working copy; they pass with LF.
- Docker images `shopee-catalog`, `shopee-inventory`, `shopee-frontend` build with `docker compose --env-file .env.example build` and run as user `app`.
- Not run locally: real MinIO/S3 storage, browser against real services.
