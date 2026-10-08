# Shipment upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/08-shipment.md).

## Cấu hình

Không có secret mới.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `SHIPMENT_CARRIER_PROVIDER` | `manual` | `manual`: vendor/admin nhập mã vận đơn và kết quả giao, yêu cầu chặn giao được xử lý bằng tay (D04). `mock`: carrier giả để test local, bị từ chối khi `ENV=production` |
| `SHIPMENT_CARRIER_MOCK_WEBHOOK_SECRET` | rỗng | Chỉ cần khi `mock` |
| `SHIPMENT_ADDRESS_RETENTION_DAYS` | `180` | Sau bao nhiêu ngày một shipment đã kết thúc bị xóa tên, số điện thoại, đường, phường của người mua. `0` là giữ lại; còn lại 30–3650. Order vẫn giữ bản địa chỉ của đơn |

## Thay đổi hành vi

- Vendor chỉ tự chuyển đơn sang `processing` ở Order. `shipped` và `completed` đến từ Shipment: vendor bấm "Đã giao cho vận chuyển" (kèm mã vận đơn) rồi "Người mua đã nhận hàng". Shipment gửi event qua outbox, Order tự chuyển trạng thái.
- Không giao được nếu Order chưa cho phép: chưa thanh toán xác minh, chưa trừ kho, hoặc đơn đã hủy/hoàn.
- Báo giá có hạn 15 phút. Shop có sản phẩm chưa khai cân nặng bị báo "không giao được" ở checkout thay vì tính phí đoán.

## Thứ tự triển khai

Order, Shipment và frontend phải lên cùng một đợt ngắn. Order mới từ chối vendor tự đánh dấu shipped, còn frontend cũ vẫn gọi đường đó; Shipment mới gửi event mà Order cũ không có endpoint nhận.

1. Backup `shipment_db`, `order_db`.
2. Chạy [shipment-preflight.sql](shipment-preflight.sql): phần đầu trên `shipment_db`, phần cuối trên `catalog_db`. Bổ sung cân nặng cho các sản phẩm được liệt kê trước khi deploy, nếu không các shop đó không bán được.
3. Build: `bash deploy/build-images.sh order shipment frontend`.
4. Migration Shipment lên `000005_fulfillment_workflow` (`make -f Makefile.txt migrate-up SERVICE=shipment`).
5. Deploy Order trước, rồi Shipment, rồi frontend: `docker compose up -d --no-deps order shipment frontend`.
6. Smoke: tạo đơn, thanh toán, vendor bấm bắt đầu xử lý, nhập mã vận đơn, đánh dấu đã nhận. Đơn chuyển `shipped` rồi `completed`; trang admin Fulfillment không có mục bị kẹt.

Shipment đang đi đường trước khi nâng cấp giữ nguyên trạng thái. Khi vendor đánh dấu đã nhận, Order chuyển vendor order sang `completed` như bình thường.

## Theo dõi

Mỗi phút Shipment ghi `shipment_operations_report` (warn khi có mục cần xử lý):

| Bộ đếm | Ý nghĩa | Xử lý |
|---|---|---|
| `fulfillment_lag` | Đã thanh toán, chưa giao vận chuyển quá 3 ngày | Nhắc vendor, hoặc hủy đơn và hoàn tiền |
| `tracking_stale` | Đang giao, không cập nhật quá 7 ngày | Hỏi hãng vận chuyển, ghi kết quả |
| `interception_pending` | Yêu cầu chặn giao chưa có kết quả quá 24 giờ | Gọi hãng, ghi "Intercepted" hoặc "Not intercepted" |
| `failed_attempts` | Có lần giao thất bại | Liên hệ người mua; ghi "Returned" nếu hàng hoàn về |
| `order_events_review` | Order từ chối event (vd đơn đã hủy) | Kiểm tra đơn, rồi gửi lại ở trang Fulfillment |

Log khác: `shipment_quote_unavailable` (kèm lý do: chưa có phương thức, ngoài vùng, chưa có bảng phí, thiếu cân nặng), `shipment_status_changed`, `shipment_interception_requested`, `shipment_carrier_webhook_rejected`, `shipment_outbox_failed`, `shipment_addresses_redacted`. Phía Order: `order_shipment_event_applied`, `order_shipment_event_refused`, `order_shipment_returned`.

## Sự cố thường gặp

- **Mất mã vận đơn hoặc nhập sai:** vendor hoặc admin sửa mã khi hàng đang đi đường, bắt buộc ghi lý do. Thao tác được lưu vào lịch sử.
- **Hãng vận chuyển ngừng hoạt động:** đơn vẫn ở `shipped`, `tracking_stale` tăng. Không đánh dấu đã giao khi chưa có xác nhận của hãng.
- **Người mua muốn đổi địa chỉ sau khi đặt:** MVP không hỗ trợ sửa địa chỉ. Trước khi giao vận chuyển, hủy đơn để hoàn tiền và người mua đặt lại. Sau khi giao vận chuyển, liên hệ hãng; nếu hàng hoàn về thì ghi "Returned".
- **Hủy đơn khi hàng đã đi:** Shipment chuyển `interception_requested` và chờ kết quả. Không coi là đã chặn được. Chặn được thì shipment `cancelled`, hàng đang quay về; nhập lại kho chỉ sau khi vendor kiểm hàng.
- **Hàng hoàn về sau khi giao thất bại:** ghi "Returned". Order không tự hoàn tiền; admin tạo hoàn tiền tranh chấp ở trang chi tiết đơn.
- **Hủy khi đã giao xong:** dùng luồng trả hàng của Order, không sửa shipment.

## Rollback

- Order về bản cũ: không có endpoint nhận event, event Shipment bị đánh dấu chờ review. Sau khi lên lại Order mới, gửi lại ở trang Fulfillment. Vendor dùng lại luồng cũ.
- Shipment về bản cũ: trạng thái `returned` không được bản cũ hiểu; xử lý các shipment `returned` trước. Event chưa gửi nằm lại trong `shipment_outbox`.
- Không xóa shipment đã giao, không gửi lại cho hãng vì rollback image. Không chạy migration down khi đã có shipment `returned` hoặc event trong outbox.

## Fence bàn giao và lệnh dừng (AF-03)

Từ bản AF-03, "Đã giao cho vận chuyển" luôn claim quyền bàn giao ở Order (`POST /internal/orders/fulfillment-grants/:id/claims`); Order từ chối khi gói có yêu cầu hủy đang mở (409 `cancellation_pending`). Order dừng gói bằng `POST /internal/shipments/by-vendor-order/:id/stops` (`stopped` / `handed_over` / `delivered`). Deploy Order trước Shipment. Chi tiết: `deploy/paid-cancellation-runbook.md`.
