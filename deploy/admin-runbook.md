# Admin upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/09-admin.md).

## Cấu hình

Không có secret mới. Admin đọc các service bằng chính token của admin đang đăng nhập, không dùng service key để gọi admin API.

| Biến (service `admin`) | Mặc định | Ý nghĩa |
|---|---|---|
| `JWT_SECRET`, `IDENTITY_SERVICE_URL`, `IDENTITY_SERVICE_KEY` | dùng chung | Xác thực token và xác minh lại vai trò admin với Identity |
| `VENDOR_SERVICE_URL` … `SHIPMENT_SERVICE_URL`, `NOTIFICATION_SERVICE_URL` | bắt buộc | Nơi đọc số liệu vận hành và audit |
| `REVIEW_SERVICE_URL` (Compose: `ADMIN_REVIEW_SERVICE_URL`) | `http://review:8091` | Để rỗng khi tắt review: audit review không được tra cứu |
| `ADMIN_UPSTREAM_TIMEOUT` | `4s` | Giới hạn mỗi lần đọc một service (500ms–30s). Quá hạn thì service đó hiện "unavailable" |

## Thay đổi hành vi và API

- Các thao tác sau **bắt buộc lý do** (400 nếu thiếu): đặt commission rule, replay side effect của Order, thử lại hoàn tiền trả hàng, replay trạng thái shop (Vendor), bật/tắt carrier, đặt fee rule. Tạo carrier/zone, thêm tỉnh nhận ghi chú tùy chọn.
- Hoàn tiền admin ở Order nhận `Idempotency-Key` (header hoặc `idempotency_key`). Gửi lại cùng key và cùng nội dung trả về hoàn tiền đã tạo; khác nội dung thì 409. Frontend tự gửi key.
- Order và Shipment từ chối thao tác admin khi không xác minh được với Identity hoặc không ghi được audit (trước đây Order bỏ qua kiểm tra khi thiếu Identity). Cấu hình shipping và kiểm duyệt review giờ xác minh lại admin với Identity.
- `X-Request-Id` từ client chỉ được giữ nếu dài 8–64 ký tự `[A-Za-z0-9._:-]`; khác đi thì gateway/service tạo id mới.
- Replay side effect của Order trả `{"replayed": false}` khi effect đã chạy lại hoặc xong, thay vì 404.

## Thứ tự triển khai

Domain service trước, Admin sau, frontend cuối, trong một đợt ngắn: frontend cũ gọi đặt commission/replay không có lý do sẽ bị 400.

1. Backup `identity_db`, `vendor_db`, `catalog_db`, `inventory_db`, `order_db`, `payment_db`, `shipment_db`, `review_db`.
2. Build toàn bộ image (đổi `backend/pkg`): `bash deploy/build-images.sh`.
3. Migration (không viết lại dữ liệu lớn; Order/Shipment backfill commission rule, hoàn tiền admin, fee rule có tác giả):
   `make -f Makefile.txt migrate-up SERVICE=<svc>` cho identity (000004), vendor (000008), catalog (000013), inventory (000007), order (000013), payment (000009), shipment (000006), review (000002).
4. Deploy các service domain: `docker compose up -d --no-deps identity vendor catalog inventory cart order payment shipment notification review gateway`.
5. Deploy `admin`, rồi `frontend`.
6. Smoke: vào `/admin` thấy các nhóm số liệu, không service nào "unavailable". Đặt lại commission rule với lý do, mở `/admin/audit` thấy dòng `commission_rule_set` có request id. Thử replay một effect đã xong: trả `replayed: false`, không có dòng audit mới.

## Vận hành

### Dashboard (`/admin`)

Mỗi ô lấy số từ service sở hữu dữ liệu, kèm thời điểm. Service không trả lời thì ô hiện "Unavailable", không hiện 0. Log `admin_source_unavailable` (kèm tên service và lý do) khi một nguồn lỗi.

| Nhóm | Ô | Nguồn |
|---|---|---|
| Moderation | Shop chờ duyệt, sản phẩm chờ duyệt, báo cáo review đang chờ (khi bật Review) | Vendor, Catalog, Review |
| Fulfillment | Đã thanh toán chưa giao, chậm giao 3 ngày, tracking không cập nhật 7 ngày, chặn giao chưa có kết quả | Order, Shipment |
| Money | Capture chưa áp vào đơn, capture Order từ chối chưa hoàn, link thanh toán kẹt, hoàn tiền chưa xác nhận, hoàn tiền trả hàng lỗi | Payment, Order |
| Stock | Hold hết hạn chưa nhả, hold lệch với Order | Inventory |
| Background jobs | Side effect Order dừng, event trạng thái shop, job Catalog, event Inventory, event Shipment bị Order từ chối, ảnh review lỗi chưa dọn, event bị park ở bên nhận (`/admin/events`, PLT-03) | Từng service |
| Notifications | Email dừng sau retry, chờ quá 15 phút, bị từ chối 24 giờ, hàng đợi Redis không truy cập được, thông báo shop bị Notification từ chối | Notification, Vendor |

### Phục hồi một capture chưa áp vào đơn (không chạy SQL)

1. Ô "Captured payments not applied to an order" > 0 → mở `/admin/payments`.
2. Tìm theo mã đơn, payment id hoặc mã provider. Xem receipt và trạng thái đồng bộ với Order.
3. Receipt `retryable`/`parked`: sửa nguyên nhân (Order ngừng, cấu hình) rồi "Retry" với lý do. Đồng bộ Order cần review: "Retry order sync" với lý do. Intent nghi ngờ: "Reconcile" để hỏi lại provider.
4. Gửi lại hai lần không tạo tác động tiền trùng: Order áp capture theo payment id một lần; Payment ghi receipt theo event id một lần.
5. Xác nhận ở `/admin/audit` (lọc theo payment id hoặc request id) và trên trang đơn.

Capture Order từ chối (đơn đã hủy, trả hai lần): ô "Captures Order refused" → trang đơn → "Refund this payment".

### Kết quả không rõ

Khi một thao tác mất phản hồi hoặc server lỗi, giao diện hiện "Result unknown" kèm request id thay vì mời gửi lại. Mở `/admin/audit?request_id=<id>`: có dòng audit nghĩa là thao tác đã áp dụng. Hoàn tiền dùng cùng Idempotency-Key nên "Send the same request again" an toàn.

### Audit (`/admin/audit`)

- Lọc theo service, actor, loại và id đối tượng, hành động, request id, khoảng thời gian; 50 dòng mỗi trang, trang sau theo cursor. Một service không trả lời thì trang ghi "incomplete", không coi là không có audit.
- Nội dung chỉ gồm id, lý do admin nhập và trường thay đổi (trạng thái, số tiền, tỷ lệ). Không chứa email, số điện thoại, địa chỉ, token.
- Lưu giữ: audit không bị xóa trong MVP. Trigger chặn sửa/xóa; không có job dọn. Khi cần chính sách xóa (D06), làm bằng migration riêng có duyệt, không xóa tay.
- Quyền: đọc audit là endpoint GET riêng, tách khỏi mọi endpoint thay đổi; Admin service không có đường ghi. Hiện mọi admin đều đọc được; vai trò "auditor" chỉ đọc cần quyết định riêng (Identity chỉ có buyer/vendor/admin).

## Rollback

- Image: quay về bản cũ được. Bản cũ ghi audit không có `request_id` (cột nullable), Order/Shipment cũ không ghi `order_admin_audit`/`shipment_admin_audit`, Admin cũ chỉ có health. Frontend cũ vẫn chạy với service mới trừ các thao tác giờ bắt buộc lý do.
- Không chạy migration down sau khi đã có audit mới: down của Order/Shipment từ chối khi có dòng có `request_id`, down của Inventory từ chối khi đã có audit nhập kho, down của Vendor từ chối khi có `event_replayed`. Không xóa audit, không lùi trạng thái tiền để rollback.
