# Giá theo SKU — rollout và rollback

Runbook này áp dụng cho AF-10. Code và migration đã được kiểm thử local ngày 2026-10-12 và diễn tập toàn trình tự trên bản sao dữ liệu ngày 2026-10-15 ([báo cáo PW-049](rehearsal/reports/pw049-2026-10-15.md)); các bước staging/pilot vẫn cần release record riêng. `FEATURE_VARIANT_PRICING_ENABLED` phải giữ `false` cho đến khi Catalog, Cart, Order và frontend cùng dùng contract mới.

## Thứ tự triển khai

1. Backup các database Catalog, Cart và Order; ghi image digest và schema version hiện tại. Dừng nếu migration đang dirty hoặc không đúng version dự kiến.
2. Trong cửa sổ bảo trì checkout, chạy migration bằng runner của dự án: Catalog lên `000017`, Cart lên `000005`, Order lên `000030`. Không chạy SQL file trực tiếp ngoài sổ migration.
3. Giữ cờ `false`. Deploy Catalog mới trước để cung cấp batch quote tương thích cho giá đã backfill, sau đó Cart và Order, cuối cùng frontend. Không mở checkout nếu một consumer còn gọi contract cũ cho SKU khác giá.
4. Chạy các truy vấn read-only bên dưới. Với cờ tắt, smoke product thường và SKU có giá bằng product; sửa giá product có variants phải đồng bộ giá/version/audit của mọi SKU và giữ hành vi bán một giá.
5. Trước khi bật: danh mục của sản phẩm pilot phải có quy tắc `pkg_weight`, vì sửa nội dung ghi lại packaging từ thuộc tính `pkg_*` và thiếu cân nặng thì không tính được phí ship. Mỗi lần sửa giá SKU đưa sản phẩm về draft; xếp admin trực duyệt lại và báo shop pilot. Sau khi duyệt, chờ `selling_sync_pending=false` ở trang sản phẩm rồi mới thử checkout (trước đó trả `variant_not_sellable`).
6. Trên staging, bật cờ Catalog toàn môi trường và chỉ dùng tài khoản/shop pilot trong cửa sổ nghiệm thu. Tạo hai SKU khác giá, gửi duyệt lại và chạy buyer flow: xem khoảng giá → chọn SKU → thêm giỏ → preview → checkout → capture đúng tổng → giao/return. Đây là cờ toàn cục, không phải allowlist theo shop; thông báo vendor hoặc giới hạn quyền truy cập staging tương ứng. Chỉ mở rộng sau khi outbox không parked và đối soát order item đúng quote.

Preflight sau migration trong Catalog — [variant-pricing-preflight.sql](variant-pricing-preflight.sql) gồm ba truy vấn dưới đây cùng kiểm tra audit lệch giá, outbox park/tồn và trigger:

```sql
SELECT count(*) AS invalid_variant_prices
FROM product_variants
WHERE price_amount IS NULL OR price_amount <= 0 OR price_version <= 0;

SELECT count(*) AS missing_backfill_audit
FROM product_variants v
LEFT JOIN variant_price_audit a
  ON a.variant_id=v.id AND a.price_version=v.price_version
WHERE a.variant_id IS NULL;

SELECT count(*) AS differentiated_while_disabled
FROM product_variants v JOIN products p ON p.id=v.product_id
WHERE v.price_amount<>p.price_amount;
```

Cả ba kết quả phải bằng 0 trước lần bật đầu. Sau khi pilot đã tạo giá khác nhau, truy vấn thứ ba tăng là bình thường và đồng nghĩa không được rollback về reader cũ.

Kiểm snapshot ở Order bằng ID synthetic của đơn pilot, không xuất PII:

```sql
SELECT pricing_source, price_amount, catalog_product_version,
       catalog_variant_version, count(*)
FROM order_items
WHERE vendor_order_id IN (
  SELECT id FROM vendor_orders WHERE order_id='<pilot-order-uuid>'
)
GROUP BY 1,2,3,4;
```

## Theo dõi và replay

`GET /api/catalog/operations` có `variant_price_pending` và `variant_price_parked`. Metric outbox dùng tên `variant_price_outbox`; pending phải quay về 0 khi NATS hoạt động. Tra cứu read-only:

```sql
SELECT variant_id, price_version, product_id, product_version,
       attempts, next_attempt_at, created_at
FROM variant_price_outbox
ORDER BY next_attempt_at, variant_id
LIMIT 100;
```

Sau khi sửa broker, admin replay một SKU bằng endpoint operations hiện có; hành động được audit:

```json
{"kind":"variant_price","id":"<variant UUID>","reason":"Đã khôi phục event bus"}
```

Listing hiện không cache giá trong Redis. Event `catalog.variant_price_changed` là invalidation reference cho projection/cache tương lai; payload không chứa tiền. Cart và Order không đọc giá từ event hay listing projection.

## Diễn tập lại

[rehearsal/variant-pricing.sh](rehearsal/variant-pricing.sh) dựng compose project riêng từ bản sao cluster đã dừng, xuất source baseline/ứng viên từ git và chạy đúng thứ tự trên: backup + restore thử, migrate, deploy, preflight, các pha E2E `legacy`, `flag-off`, `pilot`, `outage`, `rollback` và kiểm storefront. Không dùng stack, volume hay `.env` phát triển. Lệnh và giới hạn ở đầu script; kết quả lần chạy 2026-10-15 trong [báo cáo PW-049](rehearsal/reports/pw049-2026-10-15.md).

Số đo tham khảo trên bản sao khoảng 21.200 SKU: migration Catalog 3,3–3,7 s, Cart và Order dưới 0,2 s; cả trình tự chạy khoảng 10 phút khi image đã có cache. Refund của return có thể sang Payment chậm khoảng 1 phút khi effect Order retry; theo dõi `order_effects` trước khi kết luận lỗi.

## Smoke bắt buộc

- Product không variant vẫn dùng product price. Product có variants hiển thị min–max; chưa chọn SKU không có final price.
- SKU của product khác, SKU bị xóa/ẩn, giá không dương và buyer gửi giá giả đều bị từ chối; không thay bằng product price.
- Hai editor dùng cùng version chỉ một request thành công. Sửa giá theo policy hiện tại đưa product về draft, ghi audit và tăng fence.
- Giá đổi sau khi buyer thêm giỏ yêu cầu xác nhận lại. Thay quantity không cập nhật `seen_price`/version.
- Giá đổi giữa preview và create trả `409 price_changed`, không tạo order và không giữ kho.
- Capture dùng tổng Order; sửa giá SKU sau mua không đổi order item, refund/return hoặc lịch sử.
- Tắt NATS làm outbox tăng; bật lại giữ nguyên event ID và xả về 0. Không dùng event để quyết final price.

## Rollback

Trước khi có audit giá vendor hoặc SKU khác giá, down migration tự kiểm tra và có thể dùng trong rollback đã rehearsal. Sau khi đã bán hoặc đã ghi giá SKU khác nhau, không chạy down và không deploy Cart/Order/Catalog cũ: các reader đó có thể tính product price sai.

Rollback an toàn sau lần bật đầu là đặt `FEATURE_VARIANT_PRICING_ENABLED=false`, giữ schema và reader mới, rồi fix-forward. SKU đang khác product price sẽ fail closed, còn product thường và SKU bằng giá product tiếp tục bán. Không sửa đồng loạt giá SKU về product price và không xóa audit/order snapshot để ép rollback.
