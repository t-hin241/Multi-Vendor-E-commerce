# Order upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/06-order.md) và [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình

Không có secret mới. Order, Payment, Shipment, Inventory, Catalog và Review dùng `IDENTITY_SERVICE_KEY` hiện có làm service key cho mọi API nội bộ.

| Biến | Service | Mặc định | Ý nghĩa |
|---|---|---|---|
| `PAYMENT_SERVICE_URL` | Order | bắt buộc | Gửi yêu cầu hoàn tiền sang Payment. Thiếu biến này Order không khởi động |
| `ORDER_RETURN_WINDOW_DAYS` | Order | `7` (1–365) | Số ngày sau khi gói hàng hoàn tất buyer còn được yêu cầu trả hàng. Mỗi yêu cầu lưu `policy_version` dạng `window-7d` |

Compose đã khai báo cả hai biến cho Order. Order không `depends_on` Payment vì Payment đã phụ thuộc Order; yêu cầu hoàn tiền là tác vụ bền vững nên Payment lên sau vẫn được gửi lại.

## Thay đổi contract giữa các service

| Contract | Bên cung cấp | Bên gọi | Tương thích |
|---|---|---|---|
| Mọi route `/internal/*` của Order bắt buộc service key | Order | Payment, Inventory, Shipment, Catalog, Review | Bên gọi mới gửi key cho Order cũ vô hại. Catalog và Review cũ không gửi key sẽ bị 403 với Order mới |
| `POST /internal/shipments/quotes`; `/internal/shipments/*` bắt buộc service key | Shipment | Order | Order cũ không gửi key nên không tạo được shipment với Shipment mới. Xem bước 9 |
| `GET /internal/carts/:buyerId/lines` | Cart | Order | Bổ sung, không phá vỡ |
| `POST /internal/inventory/returns` | Inventory | Order | Bổ sung |
| `POST /internal/payments/refunds`, admin `GET/POST /api/payments/admin/refunds…` | Payment | Order, admin | Bổ sung |
| `mark-paid` nhận `{payment_id, amount, currency}` | Order | Payment | Order mới vẫn nhận body rỗng của Payment cũ và log `order_mark_paid_legacy_contract`. Order cũ bỏ qua body |
| `POST /internal/refund-events` | Order | Payment | Mới |
| CORS cho header `Idempotency-Key`, expose `X-Total-Count` | Gateway | Frontend | Phải lên trước frontend mới |

## Thứ tự triển khai

Build image trước bằng `bash deploy/build-images.sh`, hoặc `bash deploy/build-images.sh order payment` cho vài service. Script build từng image một và thử lại khi lỗi mạng. Không dùng `docker compose build` hay `up --build` cho toàn bộ stack: build song song dễ hết thời gian chờ với lỗi `context deadline exceeded`. Các lệnh `up` bên dưới dùng image đã build sẵn.

1. Backup `order_db`, `payment_db`, `inventory_db`, `shipment_db`, `cart_db`. Staging/production phải thử restore trước cutover.
2. Kiểm tra `schema_migrations` từng DB, dừng nếu dirty hoặc sai version.
3. Chạy [order-preflight.sql](order-preflight.sql): phần đầu trên `order_db`, phần `payment_db` trên `payment_db`. Các nhóm "Blocking" phải bằng 0. Ghi lại số liệu nhóm thông tin:
   - `refunded_orders_without_receipt`: đơn bị đánh dấu `refunded` bằng thao tác admin cũ, không có giao dịch hoàn tiền. Migration không bịa số tiền đã hoàn; đối soát với cổng thanh toán hoặc ngân hàng bằng tay.
   - `legacy_returns_awaiting_refund`: sẽ chuyển thành `approved`, tức chưa nhận hàng và chưa hoàn tiền.
   - `completed_packages`: `completed_at` được lấy từ `updated_at`, là mốc bắt đầu cửa sổ trả hàng.
   - `orders_captured_twice`: hoàn tiền cho các đơn này phải chỉ rõ payment.
4. Deploy Gateway, Catalog, Review, Cart, Inventory. Các bản này tương thích với Order cũ.
5. Migration Payment lên `000006_order_refunds` (`make migrate-up SERVICE=payment`), deploy Payment.
6. Tạm ngừng checkout nếu có thể (bảo trì ngắn), vì bước 7 và 8 cần làm liền nhau.
7. Deploy Shipment.
8. Migration Order lên `000011_refunds_returns`, chạy [order-validate.sql](order-validate.sql) (mọi dòng bằng 0), rồi deploy Order: `docker compose up -d --no-deps order`.
9. Bù shipment cho đơn được thanh toán giữa bước 7 và 8. Câu lệnh idempotent: Shipment trả lại shipment đã có của vendor order.

   ```sql
   -- order_db
   INSERT INTO order_effects (order_id, kind, target)
   SELECT order_id, 'create_shipment', id FROM vendor_orders
   WHERE status IN ('paid', 'processing')
   ON CONFLICT (order_id, kind, target) DO NOTHING;
   ```

10. Deploy frontend mới. Smoke: checkout hiển thị phí ship từng shop và tổng, đặt hàng, thanh toán mock, admin xem chi tiết đơn, vendor thấy yêu cầu trả hàng.

Nếu Order mới lên trước Shipment mới, checkout báo không giao được vì chưa có API báo giá. Không có đơn nào được tạo trong trường hợp này.

## Theo dõi

Worker Order chạy mỗi 5 giây, mỗi vòng xử lý tối đa 20 tác vụ cho từng loại: consume giỏ, side effect, khôi phục checkout. Mỗi phút ghi log backlog.

| Log | Ý nghĩa |
|---|---|
| `order_effect_backlog` (warn khi có parked hoặc pending quá 5 phút) | Side effect tồn đọng: tạo/hủy shipment, trả tồn kho, thông báo, hoàn tiền, nhập lại kho hàng trả |
| `order_effect_parked` | Tác vụ bỏ cuộc sau 25 lần hoặc bị từ chối vĩnh viễn. Xử lý nguyên nhân rồi replay ở trang admin Orders |
| `order_payment_rejected`, `order_payment_exceptions_pending` | Payment báo tiền đã thu nhưng không thanh toán được đơn: đơn đã hủy, sai số tiền, thanh toán lần hai hoặc hết giữ hàng. Hoàn tiền ở trang chi tiết đơn |
| `order_checkout_recovered`, `order_checkout_recovery_failed` | Checkout bị gián đoạn quá 2 phút được xử lý lại theo trạng thái reservation |
| `order_refund_outcome_mismatch` | Payment báo kết quả hoàn tiền không khớp số tiền hoặc tiền tệ. Không tự áp dụng; cần đối soát |
| `order_mark_paid_legacy_contract` | Payment cũ còn gửi mark-paid không có capture. Phải hết sau khi Payment mới lên |
| `payment_refund_requested`, `payment_refund_resolved` (Payment) | Hoàn tiền được nhận và được xác nhận |
| `payment_refund_sync_failed` (Payment) | Không gửi được kết quả hoàn tiền về Order |
| `inventory_return_restocked` (Inventory) | Hàng trả được nhập lại kho |

SQL read-only:

```sql
-- order_db
SELECT kind, status, count(*), min(next_attempt_at) FROM order_effects WHERE status <> 'done' GROUP BY kind, status;
SELECT id, order_id, kind, attempts, last_error FROM order_effects WHERE status = 'parked' ORDER BY updated_at LIMIT 50;
SELECT payment_id, order_id, amount, rejection_reason, received_at FROM order_payments WHERE outcome = 'rejected' ORDER BY received_at;
SELECT status, count(*), sum(amount) FROM order_refunds GROUP BY status;
SELECT status, count(*) FROM checkout_operations GROUP BY status;

-- payment_db
SELECT status, count(*), sum(amount) FROM payment_refunds GROUP BY status;
SELECT payment_refund_id, attempts, last_error FROM payment_refund_sync
WHERE delivered_at IS NULL AND (requires_review OR attempts >= 10);
```

## Quy trình hoàn tiền thủ công

Chưa có API hoàn tiền tự động với cổng thanh toán. Money chỉ được coi là đã hoàn khi có chứng từ.

1. Order tạo yêu cầu: return đã nhận hàng, tranh chấp do admin tạo, hoặc capture bị từ chối. Order gửi sang Payment. Payment kiểm tra tổng hoàn không vượt số tiền đã thu và cùng tiền tệ.
2. Operator hoàn tiền qua dashboard cổng thanh toán hoặc chuyển khoản.
3. Tại trang admin Refunds, chọn Succeeded và nhập mã tham chiếu của cổng thanh toán hoặc ngân hàng. Nếu thất bại, chọn Failed và ghi lý do. Quyền admin được xác minh lại qua Identity.
4. Payment gửi kết quả về Order. Order cộng `refunded_amount`, chuyển gói hàng sang `refunded` khi hoàn đủ, cập nhật return và gửi thông báo.

Hoàn tiền thất bại thì return chuyển `refund_failed`; admin bấm Retry refund ở trang Returns sau khi xử lý nguyên nhân.

## Rollback

Không chạy migration down khi đã có dữ liệu của luồng mới. Down xóa lịch sử hoàn tiền, audit trả hàng và chứng từ Payment. Down `000011` còn thất bại nếu một item có nhiều yêu cầu trả hàng.

- Order về bản cũ vẫn ghi được đơn và return nhờ default, sequence và trigger của migration. Hạn chế:
  - Admin cũ duyệt return sẽ lỗi, vì trạng thái `approved_awaiting_provider_refund` không còn hợp lệ.
  - Order cũ không gửi service key nên không tạo được shipment nếu Shipment giữ bản mới. Rollback Shipment cùng lúc, hoặc sau khi lên lại Order mới thì chạy câu SQL ở bước 9.
  - Side effect, checkout operation và yêu cầu hoàn tiền đang dở dừng lại, rồi tiếp tục khi Order mới lên lại.
- Payment về bản cũ: yêu cầu hoàn tiền mới từ Order bị 404 và tác vụ `request_refund` bị park. Replay sau khi Payment mới lên lại.
- Frontend về bản cũ: checkout vẫn chạy nhưng không có idempotency key hay xác nhận tổng. Admin cũ không còn nút chuyển "refunded" hợp lệ.
