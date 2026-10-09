# Vận chuyển hàng trả (AF-05)

Đặc tả: `docs/modular/add_features/05-return-shipping.md`.

Phần này hoàn thiện đoạn từ lúc sàn duyệt trả hàng tới lúc hàng thực sự về shop:

1. **Shop chọn địa chỉ nhận trả.** Chủ shop chọn một địa chỉ của shop làm nơi nhận hàng trả, kèm giờ nhận. Admin xác minh địa chỉ đó.
2. **Sàn duyệt trả hàng.** Order chụp lại địa chỉ đã xác minh, ghi ai trả phí chiều về (mặc định người bán; người mua khi đổi ý theo chính sách) và đặt hạn gửi (mặc định 7 ngày). Shipment mở một kiện hàng trả.
3. **Buyer gửi hàng.** Buyer xem hướng dẫn (địa chỉ, giờ nhận, mã trả hàng, hạn gửi), gửi hàng rồi nhập đơn vị vận chuyển và mã vận đơn. Bản này nhập mã vận đơn thủ công, chưa mua nhãn của hãng.
4. **Shop ghi phiếu nhận hàng.** Mỗi đơn vị là bán được, hỏng hoặc thiếu.
   - Toàn bộ bán được: nhập kho và tạo refund như luồng trả hàng cũ.
   - Có hàng hỏng hoặc thiếu: phần bán được vẫn nhập kho, nhưng refund chờ admin. Admin hoàn đủ tiền, hoặc xử lý tranh chấp qua hồ sơ hỗ trợ (AF-01). Không âm thầm trừ tiền.
5. **Quá hạn gửi.** Yêu cầu chuyển cho admin xem xét. Buyer vẫn gửi được và không mất quyền được hoàn tiền.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Vendor migration `000012_return_destinations` | Bảng `vendor_return_destinations`: mỗi shop một địa chỉ nhận trả, có giờ nhận, `version`, `verified_version`.<br>Audit `return_destination_set/verified/rejected`.<br>Sửa địa chỉ đang dùng làm tăng `version`, nên phải xác minh lại; xoá địa chỉ đó bị từ chối.<br>Down tự từ chối khi có dữ liệu |
| Vendor API | **Chủ shop:** `GET/PUT /api/vendor/:vendorId/return-destination {address_id, receiving_hours}`.<br>**Admin** (`moderation.manage`): `GET /api/vendor/admin/shops/:vendorId/return-destination`, `POST …/decision {version, verify, reason}`.<br>**Order:** `GET /internal/return-destinations/:vendorId`, chỉ trả địa chỉ đã xác minh (không có thì 404) |
| Shipment migration `000010_return_shipments` | Bảng `return_shipments`, trạng thái `pending_dispatch` → `in_transit` → `received` hoặc `delivery_exception`.<br>Mỗi yêu cầu trả hàng tối đa một kiện đang chạy. Không chứa địa chỉ buyer, chỉ địa chỉ của shop.<br>Down tự từ chối khi có dữ liệu |
| Shipment API (chỉ Order) | `POST /internal/shipments/return-shipments`: mở kiện, hoặc đổi địa chỉ khi kiện chưa gửi (theo `authorization_version` lớn hơn). Đã gửi thì trả 409 `destination_changed`.<br>`POST …/:id/dispatches`, `…/:id/receipts`, `…/:id/exceptions`: gọi lại cùng lệnh thì trả cùng kết quả.<br>**Admin:** `GET /api/shipments/admin/return-shipments?status=stale` |
| Order migration `000023_return_shipping` | Cột trên `return_requests`: `authorization_version`, địa chỉ chụp lại (`return_destination`), `return_fee_payer`, `return_fee_cap`, `dispatch_deadline`, `shipping_status`, `return_shipment_id`, thông tin gửi hàng + idempotency key, `dispatch_overdue_at`, `restock_quantity`, `inspection_disputed`.<br>Bảng `return_goods_receipts` (append-only).<br>Effect `authorize_return_shipment`, `dispatch_return_shipment`, `close_return_shipment`.<br>Down tự từ chối khi có dữ liệu |
| Order API | **Buyer:** `GET /api/orders/return-requests/:id/shipping-instructions` (409 `return_not_approved`), `POST …/:id/dispatches {carrier_name, tracking_number, dispatched_at, expected_version}` + `Idempotency-Key` bắt buộc, trả 202 (gửi lại cùng key trả 200; 422 `invalid_tracking`).<br>**Shop** (`returns.handle`): `POST /api/orders/vendor/return-requests/:id/goods-receipts {sellable_quantity, damaged_quantity, missing_quantity, note, expected_version}`.<br>**Admin:**<br>• `POST …/decision {approve, note, fee_payer?, fee_cap?}` (`support.manage`).<br>• `POST /api/orders/admin/return-requests/:id/shipping-authorizations {fee_payer, fee_cap, reason, expected_version}` (`support.manage`): cấp hoặc sửa hướng dẫn trước khi gửi; đã gửi thì 409 `destination_changed`; shop chưa có địa chỉ xác minh thì 409 `return_destination_missing`.<br>• `POST …/:id/goods-receipts` (`support.manage`).<br>• `POST …/:id/shipping-decisions {action: refund\|mark_lost}` (`finance.prepare`).<br>Route cũ `/receive` chỉ dùng cho yêu cầu chưa có hướng dẫn gửi |
| Notification | Mẫu `return_shipping_instructions`. "Đã hoàn tiền" vẫn là `order_refunded`, chỉ gửi khi Payment xác nhận |
| Frontend | **Buyer:** trên trang đơn, mỗi yêu cầu trả hàng có hướng dẫn và form "Báo đã gửi hàng".<br>**Shop:** trang Vận chuyển có thẻ "Địa chỉ nhận hàng trả"; trang Đơn có phiếu nhận hàng.<br>**Admin:** `/admin/returns` (chọn người trả phí khi duyệt, cấp lại hướng dẫn, ghi phiếu, hoàn đủ khi hàng hỏng/thiếu, báo thất lạc); nút "Return address" ở danh sách shop để xác minh |

## Trạng thái gửi hàng (`shipping_status`)

| Giá trị | Ý nghĩa | Ai làm tiếp |
|---|---|---|
| `destination_missing` | Đã duyệt nhưng shop chưa có địa chỉ nhận trả đã xác minh. Không tự lấy địa chỉ hiện tại của shop | Shop chọn địa chỉ → admin xác minh → admin bấm "Issue instructions" (SLA 24 giờ) |
| `awaiting_dispatch` | Đã có hướng dẫn, chờ buyer gửi | Buyer (SLA tạm dừng). Quá hạn thì `dispatch_overdue_at` được ghi và admin xem xét (24 giờ); buyer vẫn gửi được |
| `awaiting_verification` | Buyer đã báo gửi; kiện ở Shipment là `in_transit` | Shop ghi phiếu khi hàng tới. Kiện đi quá 10 ngày hiện ở danh sách `stale` của Shipment |
| `received` | Shop đã ghi phiếu | Toàn bộ bán được: refund tự tạo. Có hàng hỏng/thiếu: admin quyết định (24 giờ) |
| `lost` | Admin xác nhận kiện thất lạc trên đường về (sau khi kiểm với hãng) | Hoàn tiền qua hồ sơ hỗ trợ (AF-01). Nếu hàng vẫn tới, shop vẫn ghi phiếu được |

Trạng thái của yêu cầu trả hàng (`approved` → `received` → `refund_pending` …) giữ nguyên như cũ. Phiếu có hàng hỏng/thiếu thì yêu cầu ở `received` với `inspection_disputed = true`.

## Cờ

| Biến | Service | Mặc định | Tắt thì |
|---|---|---|---|
| `FEATURE_RETURN_SHIPPING_ENABLED` (compose `ORDER_FEATURE_RETURN_SHIPPING_ENABLED`) | Order | `false` | Duyệt trả hàng như cũ, không có hướng dẫn gửi. Yêu cầu đã có hướng dẫn vẫn tiếp tục: buyer gửi được, shop ghi phiếu được. Phiếu nhận hàng dùng được cả khi cờ tắt |
| `ORDER_RETURN_DISPATCH_DAYS` | Order | `7` (1–30) | — |
| `FEATURE_RETURN_SHIPPING_ENABLED` (compose `SHIPMENT_FEATURE_RETURN_SHIPPING_ENABLED`) | Shipment | `false` | Không mở kiện mới; kiện đã mở vẫn gửi / nhận / báo thất lạc được |

Vendor không có cờ: địa chỉ nhận trả chỉ là dữ liệu của shop.

## Rollout

1. Backup `vendor_db`, `shipment_db`, `order_db`.
2. Deploy Vendor (`000012`) và Notification (mẫu mới). Nhắn các shop pilot chọn địa chỉ nhận trả và giờ nhận. Admin gọi xác nhận từng shop rồi bấm Verify.
3. Deploy Shipment (`000010`), bật cờ Shipment.
4. Deploy Order (`000023`) với cờ tắt. Kiểm luồng trả hàng cũ không đổi.
5. Bật cờ Order ở staging rồi thử:
   - duyệt khi shop chưa xác minh → `destination_missing` → xác minh → cấp hướng dẫn;
   - buyer gửi (bấm hai lần chỉ ghi một lần) → shop ghi phiếu toàn bộ bán được → refund (AF-06) → Payment xác nhận;
   - phiếu có hàng hỏng → kho chỉ tăng phần bán được → admin hoàn đủ;
   - quá hạn gửi → hiện ở `/admin/work-items`, buyer vẫn gửi được;
   - đổi địa chỉ trước khi gửi (cấp lại hướng dẫn) và sau khi gửi (bị từ chối).
6. **Yêu cầu đã duyệt trước AF-05** không có hướng dẫn và không bị đổi tự động. Admin bấm "Issue instructions" cho những yêu cầu cần (dùng địa chỉ đã xác minh hiện tại, có audit), hoặc để shop nhận như cũ bằng phiếu nhận hàng.

**Phí chiều về:** bản này chỉ ghi ai trả (`fee_payer`) và mức hoàn tối đa (`fee_cap`). Việc hoàn phí ship thật cho buyer theo chứng từ thuộc PW-011, chưa có. Admin chọn "buyer" khi buyer đổi ý theo chính sách của đơn.

## Rollback

- Tắt cờ Order: không cấp hướng dẫn mới; yêu cầu đang chạy vẫn đi tiếp.
- Tắt cờ Shipment: không mở kiện mới. Nếu cờ Order vẫn bật, effect mở kiện sẽ park (409 `return_shipping_disabled`); bật lại cờ rồi replay effect ở `/admin/operations`.
- Không chạy down `000023`/`000010`/`000012` khi đã có dữ liệu (down tự từ chối).
- Rollback image Order về bản trước AF-05: yêu cầu có hướng dẫn sẽ dùng lại route `/receive` cũ, mất phiếu theo đơn vị. Chỉ làm khi không còn yêu cầu nào đang chờ hàng về.

## Theo dõi

- **Log Order:**
  - `order_return_shipping_authorized`, `order_return_destination_missing`, `order_return_dispatched`, `order_return_goods_received`, `order_return_shipping_decided`, `order_return_dispatch_overdue`.
  - `order_return_shipping_backlog` mỗi phút: `destination_missing`, `dispatch_overdue`, `in_transit`, `inspection_disputed`. Mức `warn` khi có việc chờ admin.
- **Log Shipment:** `shipment_return_authorized`, `shipment_return_status_changed`, `shipment_return_backlog` (`in_transit_stale` quá 10 ngày thì `warn`).
- **Audit:** Vendor `return_destination_*`; Order `return_shipping_authorized`, `return_shipping_refund`, `return_shipping_mark_lost`.
- Effect `authorize_/dispatch_/close_return_shipment` bị park hiện trong `order_effect_backlog`.

## Kiểm thử

- **Integration Order:**
  - `TestReturnShippingFromAuthorizationToRefund`: chưa có địa chỉ, version cũ, cấp lại trước khi gửi, idempotency, mã vận đơn sai, giờ gửi ở tương lai, đổi địa chỉ sau khi gửi, route cũ bị chặn, vượt số lượng, shop khác, hai người ghi phiếu cùng lúc, refund một lần, nhập kho, đóng kiện, audit, phiếu append-only.
  - `TestDisputedReturnReceiptWaitsForAnAdmin`.
  - `TestOverdueAndLostReturnParcels`.
  - Toàn bộ integration Order đạt.
- **Integration Shipment:** `TestReturnShipmentFollowsOrdersCommands`.
- **Integration Vendor:** `TestReturnDestinationNeedsVerificationOfTheCurrentVersion`.
- **Unit:** domain Order (`TestReturnShippingRules`) và Shipment; route/quyền Order, gateway.
- **Vitest:** `src/lib/return-shipping.test.ts`.
