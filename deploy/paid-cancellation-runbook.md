# Hủy gói hàng đã thanh toán trước bàn giao (AF-03)

Đặc tả: `docs/modular/add_features/03-paid-order-cancellation.md`.

Buyer có thể yêu cầu hủy một vendor order **đã thanh toán nhưng chưa bàn giao** cho vận chuyển. Shop cũng có thể báo không thể giao (hết hàng, hàng hỏng).

Admin quyết định. Khi admin duyệt, Order lần lượt:

1. dừng shipment;
2. hoàn kho đúng một lần (khi hàng chưa rời kho);
3. gửi yêu cầu refund sang Payment (đi tiếp theo AF-06).

Tiền vẫn ở trạng thái `paid` cho đến khi Payment xác nhận refund; vendor order chỉ thành `refunded` sau đó. Bản này chỉ hủy cả kiện, chưa hủy theo từng món.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Order migration `000021_paid_cancellations` | Bảng `cancellation_requests`: mỗi vendor order chỉ một yêu cầu mở; yêu cầu đã `rejected`/`resolved` là cuối, không xoá.<br>Bảng `cancellation_request_history` (append-only).<br>`vendor_orders.handover_claimed_at`/`handover_shipment_id`.<br>Refund thêm reason `cancellation`; effect thêm `stop_fulfillment`, `recover_cancelled_stock`; audit thêm `cancellation_request`.<br>Down tự từ chối khi đã có dữ liệu |
| Fence bàn giao | Shipment phải gọi `POST /internal/orders/fulfillment-grants/:vendorOrderID/claims` (chỉ Shipment) trước khi chuyển `shipped`. Order cấp quyền và tạo yêu cầu hủy đều dưới khóa đơn, nên chỉ một bên thắng:<br>• có yêu cầu mở → 409 `cancellation_pending`, không giao;<br>• đã cấp quyền bàn giao → yêu cầu hủy trả 409 `already_shipped`.<br>`GET /internal/vendor-orders/:id` cũng trả `fulfillable=false` khi có yêu cầu mở |
| Shipment | `POST /internal/shipments/by-vendor-order/:id/stops {operation_id}` trả `stopped` (chưa bàn giao: shipment bị huỷ hoặc chưa có), `handed_over` hoặc `delivered`. Lặp lại trả cùng kết quả |
| Inventory | `POST /internal/inventory/recoveries {recovery_id, product_id, variant_id, quantity}` (chỉ Order). Mỗi `recovery_id` (`cancellation:<request>:<item>`) chỉ hoàn kho một lần, movement `cancellation_restock` |
| Notification | Mẫu `cancellation_requested`, `cancellation_approved`, `cancellation_rejected`, `sla_cancellation`. Thông báo "đã hoàn tiền" vẫn là `order_refunded`, chỉ gửi khi Payment xác nhận |
| API | **Buyer:** `POST /api/orders/vendor-orders/:id/cancellation-requests {reason_code, reason}` (`Idempotency-Key` tuỳ chọn), `GET /api/orders/:id/cancellation-requests`, `GET /api/orders/cancellation-requests/:id`<br>**Vendor** (`orders.fulfill`): `POST /api/orders/vendor/:id/cancellation-requests`, `GET /api/orders/vendor/cancellation-requests?vendor_id=`<br>**Admin:** `GET /api/orders/admin/cancellation-requests?status=open`, `POST …/:id/decisions {decision: approve\|reject\|retry_refund, reason, expected_version, restock}`. Duyệt cần `finance.prepare` vì tạo refund; xem cần `support.manage` |
| Frontend | Trang đơn của buyer: khối "Hủy gói hàng đã thanh toán".<br>Trang đơn của shop: "Báo không thể giao gói này"; nút giao bị ẩn khi có yêu cầu mở.<br>Admin: `/admin/cancellations` |

## Trạng thái

| Trạng thái | Ý nghĩa |
|---|---|
| `preparing` | Đã ghi yêu cầu, fence đã bật, đang chờ Payment xác nhận hold. Duyệt lúc này trả 503 `hold_unavailable` |
| `requested` | Hold đã xác nhận, chờ admin quyết định |
| `stopping_fulfillment` | Admin đã duyệt, đang gọi Shipment dừng giao |
| `approved` | Shipment trả `stopped` |
| `refund_pending` | Đã gửi refund (merchandise + phí ship đã trả, theo 02) |
| `resolved` | Payment xác nhận refund xong; hold được nhả |
| `needs_review` | Shipment trả `handed_over`/`delivered` (admin chỉ có thể từ chối và hướng buyer sang trả hàng / AF-04), hoặc refund thất bại (admin "Retry refund"; không được từ chối vì kho có thể đã được hoàn) |
| `rejected` | Fence được bỏ, hold được nhả, buyer nhận lý do |

- **Hoàn kho:** mặc định có hoàn kho khi buyer yêu cầu, không hoàn kho khi shop báo hết/hỏng hàng. Admin đổi được lúc duyệt.
- **Hold:** khi `FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED` tắt, yêu cầu vào thẳng `requested`. Truy vấn cũ `/internal/settlements/holds` vẫn báo `cancellation_open`.
- **SLA (AF-07):** `cancellation_decision` 4 giờ từ lúc tạo (đến khi dừng xong); `cancellation_review` 24 giờ.

## Cờ

`FEATURE_PAID_CANCELLATION_ENABLED` ở Order, mặc định `false`. Tắt chỉ chặn yêu cầu mới; yêu cầu đang mở vẫn được duyệt và xử lý, và fence vẫn chặn giao.

## Rollout

1. Backup `order_db`.
2. Deploy Notification (mẫu mới), Inventory (`/recoveries`), rồi Order có migration `000021` với cờ tắt. Order có route claim ngay cả khi cờ tắt.
3. Deploy Shipment. Từ bản này `MarkShipped` luôn claim quyền bàn giao ở Order, nên **Order phải lên trước**. Kiểm việc giao bình thường vẫn chạy và log `shipment_fulfillment_stop` chưa xuất hiện.
4. Thử ở staging với nhóm shop giao thủ công. Bật cờ rồi thử các tình huống:
   - buyer yêu cầu → hold `active` → duyệt → shipment `cancelled` → kho tăng đúng một lần → refund (AF-06) → Payment xác nhận → `resolved`;
   - shop báo hết hàng → duyệt không hoàn kho;
   - shipment đã giao cho vận chuyển → yêu cầu bị chặn (`already_shipped`), hoặc vào `needs_review`.
5. Chỉ mở cờ khi Shipment đã ở bản có claim. Shipment cũ chỉ đọc `fulfillable`, nên có khe race nhỏ.

**Hàng đã rời kho nhưng trạng thái cập nhật chậm** (chế độ thủ công): nếu shop đã giao cho vận chuyển mà chưa bấm "Đã giao cho vận chuyển", Shipment sẽ trả `stopped` cho một gói thực tế đã đi. Admin xử lý khi hãng báo:

1. Liên hệ shop và hãng để chặn giao.
2. Nếu gói vẫn tới tay buyer, xử lý như trả hàng.
3. Không hoàn kho lần hai: mỗi item chỉ có một `recovery_id`.

Nhắc shop luôn bấm "Đã giao cho vận chuyển" ngay lúc bàn giao.

## Rollback

- Tắt cờ: không nhận yêu cầu mới.
- Không chạy down `000021` khi đã có yêu cầu (down tự từ chối).
- Rollback image Shipment về bản không claim là được: bản cũ vẫn đọc `fulfillable=false`, nên vẫn không giao gói có yêu cầu mở. Rollback Order về bản trước `000021` thì mất fence, nên chỉ làm khi không còn yêu cầu mở.

## Theo dõi

- **Log:**
  - Order: `order_cancellation_requested`, `order_cancellation_decided`, `order_cancellation_already_handed_over`, `order_cancellation_refund_failed`, `order_cancellation_stock_recovered`.
  - `order_cancellation_backlog` mỗi phút: `open`, `needs_review`, `stopping_or_refund_pending_over_1h`; mức `warn` khi có review hoặc bị kẹt.
  - Shipment: `shipment_fulfillment_stop`. Inventory: `inventory_recovery_restocked`.
- Effect `stop_fulfillment`/`recover_cancelled_stock` bị parked hiện trong `order_effect_backlog`.
- Audit Order `cancellation_approve` / `cancellation_reject` / `cancellation_retry_refund`.

## Kiểm thử

- **Integration Order:**
  - `TestPaidCancellationStopsRestocksAndRefundsOnce`: hai tab, fence, hold, dừng, hoàn kho một lần, buyer không được báo "đã hoàn" trước khi Payment xác nhận.
  - `TestHandoverAndCancellationRaceHasOneWinner`.
  - `TestCancellationAfterHandoverAndFailedRefundNeedReview`.
- **Shipment:** `TestStopFulfillmentBeforeAndAfterHandover`.
- **Inventory:** `TestRecoveryRestockHappensOncePerRecoveryID`.
- **Unit route/quyền:** Order, Shipment, Inventory, gateway.
- **Vitest:** `src/lib/cancellations.test.ts`.
