# Giao thất bại và hàng hoàn về (AF-04)

Đặc tả: `docs/modular/add_features/04-failed-delivery-resolution.md`.

Khi một kiện hàng không giao được, Shipment báo sự việc cho Order:

- giao thất bại đủ số lần cho phép (`attempts_exhausted`, mặc định 2 lần);
- hàng đã hoàn về shop (`returned`);
- đơn vị vận chuyển xác nhận thất lạc (`lost`, chỉ admin ghi nhận được, sau khi kiểm chứng cứ của hãng).

Order mở **hồ sơ giao thất bại** cho vendor order đó: mỗi vendor order chỉ có một hồ sơ mở, mỗi shipment chỉ mở một hồ sơ. Hồ sơ giữ payout (hold) trong sổ của Payment. Admin chọn một trong hai hướng:

- **Giao lại:** người mua xác nhận địa chỉ, không thu thêm phí. Shipment tạo lần giao mới (`attempt_no + 1`).
- **Hoàn tiền:** hoàn tiền hàng và phí ship đã trả, qua Payment (AF-06).

Shop ghi nhận hàng hoàn về theo từng đơn vị: bán lại được / hỏng / thiếu. Chỉ hàng bán lại được mới được nhập lại kho, và chỉ một lần, sau khi đã hoàn tiền. Bản này xử lý theo cả kiện; mất một phần đi theo hồ sơ hỗ trợ (AF-01).

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Shipment migration `000009_delivery_exceptions` | Trạng thái `lost`, cột `lost_at`.<br>`attempt_no`, `original_shipment_id`, `replacement_operation_id`: một gói có thể có nhiều lần giao, unique `(vendor_order_id, attempt_no)`, tối đa một lần giao đang chạy.<br>Outbox thêm `exception_attempts_exhausted` / `exception_returned` / `exception_lost` (một dòng mỗi shipment và loại).<br>Down tự từ chối khi đã có dữ liệu AF-04 |
| Order migration `000022_delivery_exceptions` | `delivery_exceptions`: một hồ sơ mở mỗi vendor order, `shipment_id` unique, `policy_snapshot` (`delivery-resolution-v1`: giao lại tối đa 1 lần, phí giao lại do shop/sàn chịu, hoàn tiền gồm phí ship), hold, địa chỉ giao lại người mua đồng ý.<br>`delivery_exception_history`, `delivery_exception_receipts`, `delivery_exception_receipt_lines`: append-only.<br>Refund reason `delivery_exception`; effect `create_replacement_attempt`, `recover_delivery_stock`; audit `delivery_exception`.<br>Down tự từ chối khi có hồ sơ |
| Event | `shipment.exception_detected` (schema 1), consumer Order `order-shipment-exceptions` (đã cấp trong `deploy/nats/nats.conf`). Khi `EVENT_PUBLISHING=http`: `POST /internal/shipment-exceptions` (chỉ Shipment) |
| Shipment API | **Shop:** `POST /api/shipments/:id/failure-reports {kind: returned, reason, expected_version}`.<br>**Admin** (`support.manage`): `POST /api/shipments/admin/shipments/:id/failure-reports {kind: returned\|lost, …}`.<br>**Order:** `POST /internal/shipments/replacement-attempts {operation_id, vendor_order_id, original_shipment_id, attempt_no, eligibility_ref, destination}` trả 202; lặp cùng `operation_id` trả cùng lần giao; 409 `active_attempt_exists`, lần giao cũ chưa kết thúc bằng returned/lost, hoặc không phải lần mới nhất. Các route cũ `/return`, `/failed-attempts` vẫn chạy và cũng sinh sự việc khi cờ bật |
| Order API | **Buyer:** `GET /api/orders/:id/delivery-exceptions`, `GET /api/orders/delivery-exceptions/:id`, `POST …/:id/redelivery-consents {accept, address_id?, expected_version}`.<br>**Shop:** `GET /api/orders/vendor/delivery-exceptions?vendor_id=&status=` (`orders.read`), `POST /api/orders/vendor/delivery-exceptions/:id/receipts {received_lines: [{item_id, quantity, condition}], note, expected_version}` (`returns.handle`).<br>**Admin:** `GET /api/orders/admin/delivery-exceptions?status=open`, `GET …/:id` (`support.manage`), `POST …/:id/receipts` (sửa phiếu, cần ghi chú; `support.manage`), `POST …/:id/decisions {resolution: redeliver\|refund\|close\|retry_refund, reason, expected_version}` (`finance.prepare`) |
| Fence bàn giao | Khi vendor order có hồ sơ mở, `fulfillment-grants/:id/claims` chỉ cấp cho lần giao lại mà Order đã ghi nhận (409 `delivery_exception_open` cho shipment khác) |
| Inventory | Recovery id `delivery_exception:<case>:<item>`, movement `delivery_return_restock`, mỗi id chỉ nhập kho một lần |
| Notification | Mẫu `delivery_exception_opened`, `delivery_redelivery_offered`, `delivery_exception_resolved`, `sla_delivery_exception`. Thông báo "đã hoàn tiền" vẫn là `order_refunded`, chỉ gửi khi Payment xác nhận |
| Frontend | Trang đơn của buyer: khối "Sự cố giao hàng" (xác nhận địa chỉ giao lại hoặc từ chối).<br>Trang đơn của shop: mục "Giao thất bại / hàng hoàn về" với phiếu nhận hàng.<br>Admin: `/admin/delivery-exceptions`; nút "Lost" ở `/admin/fulfillment` |

## Trạng thái

| Trạng thái | Ý nghĩa | Ai làm tiếp |
|---|---|---|
| `investigating` | Hết số lần giao hoặc thất lạc; admin kiểm với hãng và liên hệ buyer | Admin (24 giờ) |
| `awaiting_goods` | Hàng đang/đã hoàn về shop | Shop ghi phiếu nhận hàng (48 giờ), sau đó admin quyết định (24 giờ) |
| `awaiting_buyer` | Đã đề nghị giao lại | Buyer xác nhận địa chỉ hoặc từ chối; đồng hồ SLA tạm dừng |
| `redelivery_pending` | Đã yêu cầu Shipment tạo lần giao mới; xong khi lần giao mới `delivered` | Shop giao lại |
| `refund_pending` | Đã yêu cầu Payment hoàn tiền | Payment / AF-06 |
| `needs_review` | Các sự việc mâu thuẫn: giao thành công muộn, refund lỗi, Shipment từ chối lần giao lại, buyer từ chối giao lại | Admin |
| `resolved` | Kết thúc; hold được nhả | — |

Quy tắc:

- **Hoàn tiền** chỉ khi lần giao hiện tại đã `returned` hoặc `lost`. Gói hết số lần giao có thể vẫn tới tay buyer, nên phải chờ hãng trả hàng hoặc admin xác nhận thất lạc.
- **Giao lại** chỉ khi hàng đã về shop, phiếu nhận hàng ghi toàn bộ bán lại được, chưa chọn hoàn tiền, và chưa giao lại lần nào.
- **Hoàn tiền và giao lại** quyết định dưới khóa đơn: chỉ một bên thắng, bên kia nhận 409. Sau khi đã chọn hoàn tiền thì không giao lại được (409 `resolution_locked`).
- **Giao thành công muộn** (carrier báo `delivered` sau khi hồ sơ đã mở): hồ sơ vào `needs_review`. Vendor order vẫn hoàn tất nếu chưa hoàn tiền, nhưng hold vẫn giữ payout cho đến khi admin chọn `close`.
- **Hàng hoàn về sau khi đã hoàn tiền:** hồ sơ về `awaiting_goods` cho tới khi shop ghi phiếu. Khi đó nhập kho phần bán lại được và đóng hồ sơ.
- **Phiếu nhận hàng:** shop ghi một lần. Admin sửa bằng phiên bản mới, có ghi chú và audit. Không sửa được sau khi đã nhập kho.
- **Gói đã hoàn tiền hoặc đã hủy** khi sự việc tới: Order chỉ ghi log `order_delivery_exception_ignored`, không mở hồ sơ.

## Cờ

| Biến | Service | Mặc định | Tắt thì |
|---|---|---|---|
| `FEATURE_DELIVERY_RESOLUTION_ENABLED` (compose `SHIPMENT_FEATURE_DELIVERY_RESOLUTION_ENABLED`) | Shipment | `false` | Không báo sự việc mới; `failure-reports` và `replacement-attempts` trả 409 `delivery_resolution_disabled`. Lần giao lại đã tạo vẫn giao được |
| `SHIPMENT_DELIVERY_ATTEMPT_LIMIT` | Shipment | `2` (1–5) | — |
| `FEATURE_DELIVERY_RESOLUTION_ENABLED` (compose `ORDER_FEATURE_DELIVERY_RESOLUTION_ENABLED`) | Order | `false` | Không đề nghị giao lại mới (409 `redelivery_disabled`). Hồ sơ vẫn được ghi, hoàn tiền và đóng; buyer vẫn trả lời được đề nghị đã gửi |

## Rollout

1. Backup `order_db` và `shipment_db`.
2. Deploy Notification (mẫu mới) và Inventory (reason theo recovery id).
3. Deploy Order có migration `000022` (consumer `order-shipment-exceptions` có ngay, cờ Order tắt). Cấp quyền NATS cho durable mới **trước** khi Order khởi động (`deploy/nats/nats.conf`).
4. Deploy Shipment có migration `000009` với cờ tắt. Kiểm việc giao bình thường không đổi.
5. Bật cờ Shipment ở staging. Worker của Shipment gửi sự việc `returned` cho các gói đã hoàn về từ trước (log `shipment_exception_backfilled`), tối đa 200 gói mỗi phút. Order mở hồ sơ cho những gói vendor order còn `shipped`/`paid`/`processing` và bỏ qua gói đã hoàn tiền/hủy/hoàn tất. Không hoàn tiền tự động cho các gói này: admin nhận từng hồ sơ ở `/admin/delivery-exceptions`.
6. Thử ở staging:
   - hai lần giao thất bại → hồ sơ `investigating` → hãng trả hàng → `awaiting_goods` → shop ghi phiếu (1 bán được, 1 hỏng) → hoàn tiền → Payment xác nhận → nhập kho đúng 1 đơn vị → `resolved`;
   - hàng hoàn về, ghi toàn bộ bán được → đề nghị giao lại → buyer chọn địa chỉ khác → Shipment tạo lần giao 2 → shop giao → `delivered` → `resolved`, hold được nhả;
   - admin ghi "Lost" ở `/admin/fulfillment` với mã tham chiếu của hãng → hoàn tiền;
   - carrier báo `delivered` sau khi đã mở hồ sơ → `needs_review` → `close`.
7. Bật cờ Order (giao lại) sau khi các bước trên đạt và có người trực `finance.prepare`.

Người vận hành ghi mã tham chiếu của hãng trong lý do và đính kèm xác nhận của hãng (ảnh/video) khi ghi `lost` (xem "Chứng cứ thất lạc ở Shipment" bên dưới).

## Rollback

- Tắt cờ Shipment: không mở hồ sơ mới. Hồ sơ và lần giao lại đang chạy vẫn tiếp tục.
- Tắt cờ Order: không đề nghị giao lại mới.
- Không chạy down `000022`/`000009` khi đã có dữ liệu (down tự từ chối).
- Rollback image Order về bản trước AF-04 làm sự việc ngoại lệ bị park ở inbox (không có consumer), và fence không còn phân biệt lần giao lại. Chỉ làm khi không còn hồ sơ mở.
- Rollback image Shipment về bản trước AF-04 khi đã có lần giao thứ 2: bản cũ đọc "shipment của vendor order" không theo `attempt_no`. Chỉ làm khi không còn gói nào có hai lần giao đang mở.

## Theo dõi

- **Log Order:**
  - `order_delivery_exception_opened`, `order_delivery_exception_decided`, `order_delivery_goods_received`, `order_delivery_redelivery_answered`;
  - `order_delivery_redelivery_created`, `order_delivery_redelivery_refused`, `order_delivery_exception_redelivered`;
  - `order_delivery_exception_delivered_late`, `order_delivery_exception_refund_failed`, `order_delivery_stock_recovered`, `order_delivery_exception_ignored`.
- **`order_delivery_exception_backlog`** mỗi phút: `open`, `needs_review`, `redelivery_or_refund_pending_over_24h`, `investigating_undecided_over_24h` (hồ sơ không ai nhận). Mức `warn` khi có số khác 0.
- **Log Shipment:** `shipment_delivery_exception_detected`, `shipment_replacement_attempt_created`, `shipment_exception_backfilled`.
- **Bất đồng Order/Shipment:** sự việc bị park ở inbox Order (`/api/orders/admin/events/parked`), outbox Shipment cần review (`/admin/fulfillment`).
- **Hold lâu:** `order_delivery_exception_backlog` cộng hold `needs_review` hiển thị trên thẻ hồ sơ.
- Effect `create_replacement_attempt` / `recover_delivery_stock` bị park hiện trong `order_effect_backlog`.
- **Audit Order:** `delivery_redeliver`, `delivery_refund`, `delivery_close`, `delivery_retry_refund`, `delivery_receipt_corrected`.

## Kiểm thử

- **Integration Order:**
  - `TestReturnedPackageOneCaseReceiptRefundRestockOnce`: sự việc lặp/đồng thời chỉ mở một hồ sơ, hold, fence, phiếu đủ đơn vị, hàng hỏng không giao lại và không nhập kho, chọn hoàn tiền thì khóa giao lại, chỉ nhập kho đơn vị bán được và đúng một lần, không báo "đã hoàn" trước Payment.
  - `TestRedeliveryNeedsConsentAndResolvesOnDelivery`: cờ, consent, địa chỉ của người khác, version cũ, một lần giao lại, fence chỉ cho lần giao lại, không refund và không nhập kho.
  - `TestRefundAndRedeliveryRaceOneWins`.
  - `TestLateDeliveryGoesToReviewAndHoldsPayout`.
- **Integration Shipment:**
  - `TestDeliveryFailureFactsReachOrderOnce`: cờ tắt, giới hạn lần giao, shop không ghi `lost`, version cũ, báo đồng thời, backfill một lần.
  - `TestReplacementAttemptOncePerOperation`.
- **Integration Inventory:** `TestRecoveryRestockHappensOncePerRecoveryID` (reason `delivery_return_restock`).
- **Unit:** domain Order/Shipment, route/quyền Order/Shipment/gateway, hợp đồng event.
- **Vitest:** `src/lib/delivery-exceptions.test.ts`.

## Bổ sung nhóm D (2026-10-10)

- **Chứng cứ phiếu nhận hàng (PW-038):** phiếu (shop) và phiếu sửa (admin) nhận `evidence_ids` (tối đa 5 ảnh, tải lên như ảnh hồ sơ hỗ trợ). Ảnh lỗi hoặc không thuộc người ghi làm phiếu bị từ chối (409 `evidence_unavailable`). Xem ở `/delivery-exceptions/:exceptionID/evidence` (shop có `returns.handle`, admin `support.manage`).
- **Chứng cứ thất lạc ở Shipment (PW-038):** Shipment giữ chứng cứ của báo thất bại (`lost`/`returned`) trong bucket riêng `SHIPMENT_EVIDENCE_BUCKET` của object storage dùng chung (`SHIPMENT_EVIDENCE_STORAGE_*`; compose dùng MinIO, bucket `shipment-evidence`, tự tạo khi khởi động).
  - Tải lên trước cho đúng shipment: shop `POST /api/shipments/:id/evidence`, admin `POST /api/shipments/admin/shipments/:id/evidence` (multipart `file`; ảnh JPEG/PNG ≤ 5 MiB được mã hoá lại để bỏ metadata, video MP4 ≤ 50 MiB). Báo thất bại gửi `evidence_ids` (tối đa 5); chỉ gắn được tệp của chính người báo, cho đúng shipment, một lần, trong cùng transaction (409 `evidence_unavailable` thì cả báo cáo bị từ chối).
  - Khi đã cấu hình kho, `lost` bắt buộc có ít nhất một tệp (422 `evidence_required`). Chưa cấu hình: không nhận tải lên, `lost` không bắt buộc tệp (log `shipment_evidence_storage_not_configured` lúc khởi động).
  - Xem: `GET …/:id/evidence` và `…/evidence/:evidenceId` (shop của shipment và admin `support.manage`; tệp chưa gắn chỉ người tải thấy). Admin xem ở `/admin/fulfillment` với gói `lost`/`returned`.
  - Tệp tải lên mà không báo cáo nào dùng bị worker Shipment xoá sau 24 giờ (mỗi giờ). Chứng cứ đã gắn được giữ cùng shipment. Migration Shipment `000013_shipment_evidence` (down từ chối khi đã có chứng cứ gắn).
- **Ảnh phiếu nhận hàng giữ tối đa 30 ngày (PW-038):** ảnh của phiếu nhận hàng (giao thất bại, trả hàng), phiếu sửa và kiện trả thất lạc ở Order bị xoá 30 ngày sau bước nó chứng minh.
- **Nhắc hạn tới shop (PW-009):** stage `delivery_goods_receipt` nhắc shop khi tới mốc nhắc và khi quá hạn (khi bật `FEATURE_CASE_SLA_ENABLED` và `FEATURE_VENDOR_ACTION_NOTICES_ENABLED`).
