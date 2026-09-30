# Inventory upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/04-inventory.md) và [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình

Không có secret mới. Inventory dùng `IDENTITY_SERVICE_KEY` hiện có cho API nội bộ và lời gọi sang Order/Catalog/Identity.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `ORDER_SERVICE_URL` | `http://order:8086` | Đối soát trạng thái đơn và gửi sự kiện `ReservationExpired` |
| `INVENTORY_EXPIRY_ENABLED` | `false` | Bật worker tự hết hạn hold. Chỉ bật sau bước 7 |

## Thứ tự triển khai

Contract reserve/commit/release nay trả receipt có `status`. Order mới từ chối phản hồi không có receipt, nên Inventory phải lên trước Order. Order cũ vẫn chạy được với Inventory mới.

1. Backup `inventory_db`, `order_db`, `payment_db`. Staging/production phải thử restore trước cutover.
2. Kiểm tra `schema_migrations` từng DB, dừng nếu dirty.
3. Chạy [inventory-preflight.sql](inventory-preflight.sql) trên `inventory_db`. Bốn nhóm đầu phải bằng 0. Migration `000005` tự dừng nếu còn dòng reservation trùng hoặc một order có nhiều trạng thái. Không sửa bằng SQL tay khi chưa đối soát với Order. Bảng thông tin cuối cho biết số hold còn `active` và số đã quá hạn.
4. Migration Inventory lên `000006_stock_counts` bằng migration runner (`make migrate-up SERVICE=inventory`). Hold cũ được chuyển thành operation `legacy`; worker không tự hết hạn chúng.
5. Chạy [inventory-validate.sql](inventory-validate.sql). Lỗi nghĩa là có item vượt phạm vi BIGINT; dữ liệu mới vẫn đã được chặn.
6. Migration Payment lên `000005_inventory_outcome_sync`. Deploy theo thứ tự: Inventory, Order, Payment, rồi frontend. Giữ `INVENTORY_EXPIRY_ENABLED=false`.
7. Admin mở màn hình đối soát tồn kho (trang yêu cầu admin), xử lý từng hold `legacy`:
   - Order đã hủy: `release_cancelled`.
   - Order còn `pending_payment` và hold chưa quá hạn: `adopt_legacy` để worker quản lý.
   - Order đã thanh toán: không dùng công cụ này; để Order commit qua luồng thanh toán hoặc đưa vào đối soát tiền.
8. Dry-run expiry: chỉ số `overdue` là số hold worker sẽ hết hạn ngay khi bật. Khi đã chấp nhận, đặt `INVENTORY_EXPIRY_ENABLED=true` và `docker compose up -d --no-deps inventory`.

## Theo dõi

Admin API `GET /api/inventory/admin/operations` trả các bộ đếm; màn hình admin làm mới 15 giây một lần. Worker Inventory log mỗi phút:

| Log | Ý nghĩa |
|---|---|
| `inventory_operations_alert` (warn) | Có `overdue`, `expiry_parked`, `events_parked`, `cache_parked`, `order_mismatches` hoặc `reserved_mismatches` khác 0 |
| `inventory_commit_conflict` (warn) | Commit bị từ chối: hold đã hết hạn, đã release hoặc chưa từng reserve. Tiền có thể đã thu, cần đối soát/hoàn tiền |
| `inventory_expiry_failed`, `inventory_outbox_failed`, `inventory_cache_sync_failed`, `inventory_reconciliation_failed` | Lỗi của từng vòng worker |

Payment log `payment_order_sync_failed`; kết quả Order từ chối được đánh dấu `requires_review`.

SQL read-only:

```sql
-- inventory_db
SELECT status, legacy, count(*), min(expires_at) FROM reservation_operations GROUP BY status, legacy;
SELECT order_id, status, reconciliation_issue, expiry_attempts FROM reservation_operations
WHERE reconciliation_issue IS NOT NULL OR (status = 'held' AND expires_at <= now()) ORDER BY created_at LIMIT 50;
SELECT order_id, attempts, next_attempt_at, last_error FROM inventory_outbox WHERE delivered_at IS NULL ORDER BY next_attempt_at LIMIT 50;

-- payment_db
SELECT payment_intent_id, outcome, attempts, last_error FROM payment_order_sync
WHERE delivered_at IS NULL AND (requires_review OR attempts >= 10);
```

## Sửa lỗi có audit

`POST /api/inventory/admin/operations/repair` với `{order_id, action, reason}`. Mọi thao tác ghi `inventory_operation_audit` kèm người thực hiện. Quyền admin được xác minh lại qua Identity.

| action | Điều kiện | Tác dụng |
|---|---|---|
| `replay` | bất kỳ | Reset retry của sự kiện và expiry cho operation |
| `release_cancelled` | Order đang `cancelled` | Trả hàng đang giữ về available; từ chối nếu đã commit |
| `adopt_legacy` | Order `pending_payment`, hold cũ chưa quá hạn | Đưa hold cũ vào worker expiry |

Không có thao tác sửa `reserved_quantity` trực tiếp. Lệch số reserved cần điều tra theo `stock_movements`.

## Smoke sau deploy

- Hai buyer cùng mua đơn vị cuối cùng: chỉ một đơn giữ được hàng, đơn còn lại báo không đủ hàng.
- Checkout, hủy đơn: hàng về available. Hủy lần hai không tạo movement mới.
- Thanh toán thành công: commit một lần; webhook lặp không trừ thêm.
- Bật expiry trên staging, để đơn quá 30 phút: hold `expired`, Order chuyển `cancelled`, thanh toán đến sau bị đưa vào review.
- Vendor gửi yêu cầu bổ sung, admin duyệt hai lần: tồn kho chỉ tăng một lần.
- Vendor kiểm kê ghi giảm; số đếm thấp hơn số đang giữ cho đơn hoặc cao hơn tồn kho bị từ chối.
- Gọi `/internal/inventory/...` không có service key bị 403.

## Rollback

- Tạm dừng expiry bằng `INVENTORY_EXPIRY_ENABLED=false` rồi restart Inventory; receipt và movement giữ nguyên.
- Không chạy migration down `000005`: nó xóa operation, outbox và audit, làm mất bằng chứng terminal state. Không hoàn tác tồn kho bằng migration.
- Migration down `000006` chỉ xóa bảng kiểm kê; movement `stock_count` vẫn còn trong sổ.
- Image Order cũ không kiểm tra receipt, image Inventory cũ không có state machine. Rollback Order trước Inventory, và chỉ khi đã chấp nhận mất các bảo vệ này.
