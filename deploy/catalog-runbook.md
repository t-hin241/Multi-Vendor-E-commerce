# Catalog upgrade — local testing và rollout

Xem [chi tiết module](../docs/module-details/03-catalog.md) và [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình và tương thích

Không thêm secret hay biến môi trường mới. Tiếp tục dùng `IDENTITY_SERVICE_KEY`, Identity/Vendor/Order/Inventory service URLs và object storage config hiện có. Compose đã chia sẻ service key cho Catalog, Cart, Order, Inventory. Không in secret/connection string ra console và không commit `.env`.

Catalog internal products nay yêu cầu service key. Order mới yêu cầu product version và read model có ACK. Cần cập nhật đồng bộ Catalog, Order, Cart, Inventory; không chỉ rebuild Catalog rồi cho checkout chạy với consumer cũ.

## Preflight và migration

Ngày 2026-09-29 đã áp dụng migration lên DB ứng dụng local: Catalog 11 → 12, Order 7 → 8, cả hai clean. Cả 7 nhóm preflight bằng 0; 4 constraint mới đã VALIDATE thành công. Số bản ghi products/variants/media/audit và orders/order_items trước/sau không đổi. Backup cùng manifest ở `.local-backups/catalog-upgrade/20260929T124735Z/`; đã kiểm tra đọc đầy đủ archive, chưa restore rehearsal. Hai container cũ đã khởi động lại và healthy, chưa recreate bằng image mới. Có 6.194 sản phẩm chờ ACK khi triển khai worker mới. Các bước dưới đây dành cho rollout môi trường khác; không chạy lại migration đã áp dụng trên local.

1. Chuẩn bị image/tag quay lui tương thích, backup `catalog_db` và `order_db`; staging/production phải thử restore trước cutover. Giữ maintenance cho checkout và mutation Catalog trong thời gian triển khai.
2. Kiểm tra `schema_migrations` của từng DB; dừng nếu dirty hoặc version không như dự kiến. Chạy [catalog-preflight.sql](catalog-preflight.sql) bằng kết nối cấu hình an toàn tới Catalog. Nếu có lỗi legacy, lập sửa dữ liệu có kiểm chứng; không tự xóa variant/thuộc tính/đơn.
3. Dùng migration runner hiện có với secret từ môi trường: Order lên `000008_product_sale_status`, Catalog lên `000012_catalog_integrity`. Không chạy trực tiếp file up mà bỏ qua sổ version của migration runner.
4. Chạy [catalog-validate.sql](catalog-validate.sql) trên Catalog để VALIDATE các constraint mới. Script có lock timeout 5 giây và statement timeout 60 giây; nếu timeout có thể thử lại trong cửa sổ bảo trì. Không mở traffic khi dữ liệu vi phạm chưa xử lý.
5. Deploy Order, Cart và Inventory mới; sau đó Catalog, cuối cùng frontend. Tránh để Compose tự khởi động dependency cũ ngoài thứ tự dự kiến; chỉ định service và dùng `--no-deps` khi dependencies đã sẵn sàng.
6. Chờ Catalog backfill + ACK. Kiểm tra `SELECT count(*) FROM products WHERE enforced_version < version;` trong Catalog phải về 0, `status_pending/status_parked` về 0. Đồng thời kiểm tra Vendor status reconciliation đang hoạt động. Không sửa trực tiếp `enforced_version` để bỏ qua ACK.
7. Chạy smoke bên dưới rồi mở traffic. Catalog có thể hiển thị sản phẩm trước khi Order ACK, nhưng internal sellability vẫn chặn checkout trong lúc chưa đồng bộ.

Build image trước cửa sổ migration, từ gốc repo:

Ngày 2026-09-29 đã chạy build thành công cả 5 image dưới đây trên local. Container đang chạy vẫn dùng bản cũ cho đến bước recreate sau migration.

```powershell
docker compose build catalog order cart inventory frontend
```

Sau migration và khi dependencies đang chạy, vẫn giữ maintenance:

```powershell
docker compose up -d --no-deps order cart inventory
docker compose up -d --no-deps catalog
docker compose up -d --no-deps frontend
```

Các lệnh `up` này là hướng dẫn vận hành; đợt thay đổi source chưa thực thi chúng trên ứng dụng local.

## Theo dõi và replay

Admin gọi `GET /api/catalog/operations`. Theo dõi `status_pending`, `status_parked`, `cleanup_pending`, `cleanup_parked`, `status_delivery_failures`, `cleanup_failures` và ba chỉ số cache stale. Cleanup pending trong thời gian grace là bình thường; status pending kéo dài làm ảnh hưởng checkout.

Sau khi sửa dependency, gọi `POST /api/catalog/operations/replay` qua admin session:

```json
{"kind":"status","id":"<product UUID>","reason":"Đã khôi phục kết nối Order"}
```

Với cleanup, dùng `kind=cleanup`, `id` là object key của job cần xử lý. Replay reset retry và ghi audit; không bỏ qua grace tối thiểu. SQL read-only tra job khi cần:

```sql
SELECT product_id, attempts, next_attempt_at, last_error FROM product_status_outbox ORDER BY next_attempt_at LIMIT 100;
SELECT object_key, product_id, attempts, next_attempt_at, last_error FROM catalog_object_cleanup ORDER BY next_attempt_at LIMIT 100;
```

Log worker: `catalog_status_backfill_failed`, `catalog_status_dispatch_failed`, `catalog_cleanup_failed`; lỗi delivery/delete đã lưu `last_error` và attempts trong queue. Log cache có operation và request ID của request khi có. Không log secret/body nhạy cảm.

## Smoke và rollback

- Vendor tạo draft có attributes/packaging/variants → upload → thiết lập stock → submit → admin approve → chờ sync → buyer xem và checkout được.
- Sai ownership/role/internal key bị từ chối. Draft/rejected/inactive hoặc shop suspended không bán được.
- Sửa sản phẩm approved đưa về draft; stale editor conflict; nộp lại/duyệt lại được. Bật tắt bán không mất lịch sử audit hay đơn cũ.
- Đang checkout thì ẩn sản phẩm: sau ACK không tạo order bằng version cũ. Tắt Order tạm thời khiến pending, không giả báo đã đồng bộ; phục hồi và replay được.
- MIME giả/ảnh hỏng/quá pixel bị chặn; upload lỗi metadata không mất cleanup intent. Không rút grace trên môi trường có dữ liệu thật để thử xóa ảnh.
- Nhiều trang/giá bằng nhau có thứ tự ổn định; outage Inventory không biến cache thành cam kết stock; checkout vẫn reserve stock thật.
- Kiểm tra đơn có variant, payment và fulfillment toàn stack theo checklist chung.

Rollback ưu tiên fix-forward hoặc bộ image tương thích contract mới; giữ schema, object, audit, read model và snapshot Order. Không dùng migration down cho dữ liệu thương mại. Image Order cũ bỏ qua product fence, Catalog cũ không bảo vệ internal API và không có re-review mới: không mở lại traffic với bộ image cũ chưa đánh giá tương thích. Nếu chưa có bản tương thích, giữ maintenance và phục hồi theo backup đã rehearsal.

## Test PostgreSQL cô lập

```powershell
docker compose -f deploy/identity-test.compose.yml up -d --wait postgres
$env:CATALOG_TEST_DATABASE_URL = 'postgres://identity_test@localhost:55431/identity_test?sslmode=disable'
go -C backend test -count=1 ./services/catalog/... ./services/order/... ./services/cart/... ./services/inventory/... ./pkg/productsales/...
docker compose -f deploy/identity-test.compose.yml stop postgres
```

Test chỉ chấp nhận DB tên kết thúc `_test`, tạo/xóa schema riêng. CI chạy PostgreSQL + race detector. Không trỏ biến test tới DB ứng dụng.
