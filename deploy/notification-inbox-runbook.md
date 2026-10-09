# Hộp thông báo người dùng (AF-09)

Đặc tả: `docs/modular/add_features/09-user-notification-inbox.md`.

Mọi người dùng đã đăng nhập (buyer, shop, admin) có chuông thông báo trên header và trang `/notifications`. Họ xem được những việc đã xảy ra ngay cả khi không đọc email, đánh dấu đã đọc, ẩn tin, rồi mở trang liên quan.

Inbox chỉ là bản đọc của các thông báo mà Notification đã ghi:

- Inbox không xác nhận tiền hay giao hàng. Số tiền và trạng thái chính xác luôn xem ở trang chi tiết; trang đó kiểm quyền lại khi mở.
- Nội dung là văn bản thuần, lấy từ cùng mẫu với email, bỏ lời chào. Không có HTML.
- Link chỉ là đường dẫn trong app (`/orders/...`, `/vendor/orders`, `/admin/...`), không phải URL tuyệt đối hay credential.

## Luồng

1. Khi Notification ghi một thông báo mới, nó ghi inbox item trong **cùng transaction**:
   - thông báo cho buyer từ Order;
   - thông báo shop từ Vendor;
   - thông báo công việc AF-08;
   - nhắc SLA cho admin.

   Item là duy nhất theo (source, event, người nhận, loại). Event gửi lặp không tạo item mới và không làm item đã đọc thành chưa đọc.
2. Email vẫn đi theo hàng đợi như cũ. Trạng thái "đã gửi" của email không phải "đã đọc".
3. Chuông hỏi số tin chưa đọc mỗi 30 giây khi tab đang mở (TanStack Query tự dừng khi tab ẩn). Cache theo user và bị xóa khi đăng xuất.
4. Bấm một tin: đánh dấu đã đọc (lặp lại vẫn giữ thời điểm đọc đầu), rồi mở link.
5. "Đánh dấu tất cả đã đọc" gửi id của tin mới nhất người dùng đã tải. Server chỉ đánh dấu các tin tới mốc đó, nên tin đến sau vẫn chưa đọc.
6. "Ẩn" đặt `hidden_at`: tin rời khỏi danh sách nhưng vẫn lưu tới khi hết hạn.
7. Mỗi giờ, vòng bảo trì xóa tin cũ hơn `INBOX_RETENTION_DAYS`, theo lô 1000, tối đa 20 lô. Bản ghi gửi email (`notifications`) có vòng đời riêng và không bị xóa theo.

## API (Notification, cần đăng nhập, `Cache-Control: private, no-store`)

| Route | Ý nghĩa |
|---|---|
| `GET /api/notifications/inbox?limit=&cursor=&unread_only=true` | `{items, next_cursor, as_of}`, mới nhất trước, `limit` 1–100 (mặc định 20); cursor không thay quyền |
| `GET /api/notifications/inbox/unread-count` | `{unread_count}` |
| `PUT /api/notifications/inbox/:id/read` | `read_at`; tin của người khác trả 404 |
| `DELETE /api/notifications/inbox/:id` | `hidden_at` |
| `POST /api/notifications/inbox/read-markers {through_id}` | `{affected, unread_count}` |
| `GET/PATCH /api/notifications/preferences` | Thêm `marketing_opt_in` (cùng `optional_vendor_categories` của AF-08, `expected_version`). Sai version trả 409 `preference_version_conflict`. Mỗi lần đổi đồng ý ghi vào `notification_consent_audit` (append-only) kèm thời điểm đồng ý hoặc rút lại |

Cờ tắt thì các route inbox trả 404 `feature_disabled` và chuông ẩn đi.

## Cấu hình

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `FEATURE_NOTIFICATION_INBOX_ENABLED` | `false` | Bật: ghi inbox cho mỗi thông báo mới và mở API, chuông. Tắt: không ghi tin mới, API trả `feature_disabled`; email không bị ảnh hưởng |
| `INBOX_RETENTION_DAYS` | `90` (7–3650) | Tuổi tối đa của tin; xóa cả khi cờ tắt |

Số tin chưa đọc được đếm trực tiếp trong PostgreSQL bằng partial index `(recipient_id) WHERE read_at IS NULL AND hidden_at IS NULL`. Không cache Redis: user Redis của Notification chỉ được dùng key `asynq:*` trên instance hàng đợi.

## Rollout

1. Backup `notification_db`.
2. Deploy Notification (`000006`) với cờ tắt. Kiểm email vẫn gửi như cũ.
3. Deploy frontend. Chuông chưa hiện vì API trả `feature_disabled`.
4. Bật cờ ở staging rồi thử:
   - buyer A không thấy tin của buyer B (thử đoán id → 404);
   - đặt đơn → có tin "Đơn hàng … đã được thanh toán" → bấm → mở đúng trang đơn, chuông giảm;
   - bấm "Đánh dấu tất cả đã đọc" trong lúc có thông báo mới tới → tin mới vẫn chưa đọc;
   - đăng xuất rồi đăng nhập người khác trên cùng trình duyệt → không thấy tin cũ;
   - điện thoại: chuông cạnh nút menu, danh sách không tràn ngang;
   - bật rồi tắt "Nhận email khuyến mãi" → hai dòng audit.
5. Bật ở production. Không backfill: tin trước khi bật không xuất hiện (không giả "chưa đọc" hàng loạt).

## Theo dõi

- Log: `notification_inbox_purged`, `notification_inbox_purge_failed`, `notification_preferences_updated`.
- Kiểm số đếm: `SELECT recipient_id, count(*) FROM inbox_items WHERE read_at IS NULL AND hidden_at IS NULL GROUP BY 1 ORDER BY 2 DESC LIMIT 20`. Số rất lớn thường là admin nhận nhắc SLA; xem lại phân công.
- Độ trễ API: log request của Notification theo route `/api/notifications/inbox*`.

## Rollback

- Tắt cờ: chuông ẩn, không ghi tin mới, email vẫn gửi. Tin đã ghi giữ nguyên và vẫn hết hạn theo lịch.
- Image Notification bản cũ chạy được trên schema mới (bảng mới không dùng, cột mới có default).
- Không chạy migration down khi đã có dòng đồng ý marketing: down tự từ chối, vì audit không bị xóa.
