# Cart upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/05-cart.md) và [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình

Không có secret mới. Cart dùng `IDENTITY_SERVICE_KEY` hiện có cho cả API nội bộ nhận từ Order lẫn lời gọi sang Catalog/Inventory. Biến mới, đã có trong `.env.example`:

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `INVENTORY_SERVICE_URL` | bắt buộc | Đọc tồn kho để cảnh báo trong giỏ; chỉ hiển thị |
| `CART_RETENTION_ENABLED` | `true` | Bật worker dọn giỏ idle |
| `CART_RETENTION_DAYS` | `180` (tối thiểu 30) | Giỏ không đổi quá số ngày này bị xóa |
| `CART_OPERATION_RETENTION_DAYS` | `90` (tối thiểu 30) | Snapshot/receipt checkout cũ hơn bị xóa |
| `CART_RETENTION_INTERVAL_MINUTES` | `60` | Chu kỳ worker |

Order không có biến mới; client Cart của Order nay gửi service key thay vì token buyer.

## Thứ tự triển khai

Contract consume phải có ở Cart trước khi Order gọi. Cart mới vẫn giữ `GET /api/cart` (trả thêm field, không bỏ field cũ) và `DELETE /api/cart`, nên Order cũ chạy được trong lúc cutover.

1. Backup `cart_db` và `order_db`. Staging/production phải thử restore trước cutover.
2. Kiểm tra `schema_migrations` của hai DB, dừng nếu dirty hoặc sai version dự kiến.
3. Chạy [cart-preflight.sql](cart-preflight.sql) trên `cart_db` bằng kết nối cấu hình an toàn. Nhóm đầu phải bằng 0. Nhóm thông tin:
   - `quantity_over_limit`: dòng cũ quá 999. Cần quyết định có kiểm soát trước bước 5, không tự sửa.
   - `idle_carts_180_days`: số giỏ worker sẽ xóa ở lần chạy đầu. Nếu chưa chấp nhận, đặt `CART_RETENTION_ENABLED=false` cho lần deploy đầu.
4. Migration Cart lên `000004_cart_versioning` bằng migration runner hiện có (`make migrate-up SERVICE=cart`). Không chạy file up trực tiếp.
5. Chạy [cart-validate.sql](cart-validate.sql). Lỗi nghĩa là còn dòng quá 999; dữ liệu mới vẫn đã được chặn.
6. Deploy Cart mới: `docker compose build cart` rồi `docker compose up -d --no-deps cart`. Smoke giỏ hàng bằng frontend cũ.
7. Migration Order lên `000009_cart_consumptions`, deploy Order mới (`--no-deps order`).
8. Deploy frontend mới (gửi `cart_version`, hiển thị trạng thái dòng và xác nhận giá).

Nếu Order mới lên trước Cart mới, checkout lỗi 404 ở bước snapshot. Không có đơn nào được tạo trong trường hợp này.

## Theo dõi

- Order log `order_cart_consume_backlog` mỗi phút khi còn tác vụ. Mức warn khi có `parked` hoặc `pending` cũ hơn 5 phút.
- Order log `order_cart_consume_retry` (Cart tạm lỗi) và `order_cart_consume_parked` (Cart từ chối hoặc hết 25 lần retry, khoảng 9 giờ).
- Cart log `cart_lookup_degraded` khi Catalog/Inventory lỗi hoặc lookup giỏ quá 1 giây, kèm `request_id`, `duration_ms`, số lỗi.
- Cart log `cart_consume_conflict` khi cùng operation bị consume lại với dòng khác, và `cart_checkout_consumed` cho mỗi consume.

SQL read-only trên `order_db`:

```sql
SELECT status, count(*), min(created_at) AS oldest FROM cart_consumptions
WHERE status IN ('held','pending','parked') GROUP BY status;

SELECT order_id, operation_id, attempts, next_attempt_at, last_error
FROM cart_consumptions WHERE status = 'parked' ORDER BY updated_at DESC LIMIT 50;
```

SQL read-only trên `cart_db`:

```sql
SELECT count(*) FILTER (WHERE consumed_at IS NULL) AS open_operations,
       count(*) FILTER (WHERE consumed_at IS NOT NULL) AS consumed_operations
FROM cart_checkout_operations;
```

## Xử lý tác vụ parked

Tác vụ parked không ảnh hưởng đơn hàng; chỉ là giỏ của buyer chưa được dọn các dòng đã mua. Buyer vẫn đặt đơn mới được. Sau khi sửa nguyên nhân (ví dụ cấu hình service key), đưa về pending bằng thao tác có ghi nhận:

```sql
UPDATE cart_consumptions SET status = 'pending', attempts = 0, next_attempt_at = now(), updated_at = now()
WHERE order_id = '<order UUID>' AND status = 'parked';
```

Consume idempotent theo operation, chạy lại không xóa thêm dòng. Nếu Cart trả 404 vì operation đã hết retention, để nguyên trạng thái parked và báo buyer tự bỏ dòng khỏi giỏ.

## Smoke sau deploy

- Buyer thêm sản phẩm, đổi số lượng, xóa dòng. Tab thứ hai sửa giỏ thì tab đầu nhận thông báo giỏ đã đổi và tự tải lại.
- Số lượng 0, âm, 1000, variant không thuộc sản phẩm, sản phẩm khác currency đều bị từ chối. Dòng thứ 51 bị từ chối.
- Vendor đổi giá: giỏ báo tăng/giảm giá, nút đặt hàng khóa đến khi buyer xác nhận giá mới.
- Tắt bán sản phẩm hoặc để hết hàng: dòng vẫn hiện, có hướng dẫn, không vào tạm tính.
- Checkout thành công: chỉ dòng đã mua biến mất. Thêm sản phẩm ở tab khác trong lúc checkout thì sản phẩm đó còn lại.
- Tạm dừng Cart ngay sau khi Order tạo đơn: đơn vẫn còn, lần checkout tiếp theo bị từ chối với hướng dẫn xem đơn hàng. Bật lại Cart thì worker consume và buyer mua tiếp được.
- Gọi `/internal/carts/...` không có service key bị 403.

## Rollback

Ưu tiên fix-forward. Không chạy `DELETE /api/cart` hay xóa hàng loạt `cart_items` để "sửa" trạng thái giỏ.

- Order: image cũ bỏ qua bảng `cart_consumptions` và quay lại xóa toàn giỏ bằng token buyer, tức mất bảo vệ race. Trước khi rollback Order, chờ `pending`/`held` về 0 hoặc chấp nhận để các tác vụ đó dừng. Không chạy migration down `000009` khi còn dòng `held`/`pending`.
- Cart: image cũ bỏ qua các cột mới và vẫn phục vụ buyer, nhưng không có API nội bộ nên Order mới sẽ lỗi. Phải rollback Order trước Cart.
- Migration down `000004` xóa snapshot và receipt. Chỉ dùng khi không còn Order mới nào chạy.
