# Notification upgrade — rollout, vận hành và rollback

> Từ đợt Platform (PLT-03): Order và Vendor gửi yêu cầu thông báo qua event bus (`order.notification_requested`, `vendor.notification_requested`, consumer `notification-requests`); route `POST /internal/notifications` chỉ còn dùng khi `EVENT_PUBLISHING=http` và chỉ nhận Order, Vendor (khóa riêng từng service). Xem [platform-runbook](platform-runbook.md).

Xem [chi tiết module](../docs/module-details/10-notification.md).

## Cấu hình

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `NOTIFICATION_EMAIL_PROVIDER` | rỗng | `smtp` hoặc `mock`. Rỗng: `smtp` khi có `SMTP_HOST`, ngược lại `mock`. `mock` chỉ ghi log loại và mã tham chiếu, bị từ chối khi `ENV=production` |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | — | Relay SMTP, bắt buộc STARTTLS (trừ `SMTP_ALLOW_PLAINTEXT=true` cho mailbox local). Production bắt buộc host và sender |
| `NOTIFICATION_ATTEMPT_RETENTION_DAYS` | `90` | Giữ lịch sử từng lần gửi bao lâu (`0` giữ mãi, còn lại 7–3650). Dòng thông báo với trạng thái cuối được giữ |
| `NOTIFICATION_DELIVERY_PAUSED` | `false` | `true`: worker không chạy, vẫn nhận, ghi yêu cầu và đẩy job; bật lại thì gửi tiếp |
| `NOTIFICATION_WORKER_CONCURRENCY` | `10` | Số job gửi chạy song song mỗi instance (1–100) |
| `REDIS_URL` | dùng chung | Hàng đợi job Asynq (queue `notification`). Đã có sẵn trong cấu hình chung |
| `IDENTITY_SERVICE_KEY` | dùng chung | Giờ **bắt buộc** (≥ 32 ký tự): Order/Vendor phải gửi key này khi gọi `/internal/notifications` |

Không có secret mới. `SMTP_PASSWORD` chỉ đọc ở config layer, không ghi log.

## Thay đổi hành vi

- `POST /internal/notifications` cần service key (trước đây mở) và trả **202** ngay sau khi ghi `pending`; việc gửi chạy bằng job Asynq trên Redis. PostgreSQL vẫn là nguồn gốc: job chỉ có id notification và số lượt, job mất được đẩy lại từ PostgreSQL. Payload thêm `event_id`, `source`, `correlation_id` (tùy chọn). Không có `event_id` thì sự kiện được xác định bằng `type` + `reference_id`.
- Gửi lặp cùng sự kiện không tạo thêm email (dedup theo nguồn, event, người nhận, loại, version template).
- Order gửi id của effect làm `event_id` và giờ kiểm tra mã trả về: 4xx thì effect park, lỗi khác thì retry. Trước đây mọi lỗi đều bị coi là thành công.
- Vendor ghi thông báo duyệt/từ chối vào `vendor_notification_outbox` cùng transaction với quyết định; worker gửi sang Notification. Trước đây gửi sau commit, mất nếu Notification lỗi.
- Nội dung email là template tiếng Việt cho: đã thanh toán, đã giao cho vận chuyển, giao thành công, đơn bị hủy, hoàn tiền đã xác nhận, shop được duyệt hoặc bị từ chối. Email không có link chứa token.

## Thứ tự triển khai

Producer trước, Notification sau: Order/Vendor mới gửi kèm service key mà Notification cũ chấp nhận. Notification mới từ chối producer cũ (không có key).

1. Backup `notification_db`, `vendor_db`.
2. Bật persistence cho Redis (cấu hình mới trong `docker-compose.yml`: AOF + `noeviction`): `docker compose up -d redis`. Redis khởi động lại và **có thể không giữ dữ liệu cũ**; hiện Redis chỉ chứa bộ đếm rate limit (hết hạn sau 60 giây) nên không mất gì quan trọng. Kiểm tra: `docker compose exec redis redis-cli CONFIG GET appendonly` trả `yes`, `CONFIG GET maxmemory-policy` trả `noeviction`.
3. Build: `bash deploy/build-images.sh order vendor notification admin frontend`.
4. Migration: Vendor `000009_notification_outbox`, Notification `000003_delivery_queue`.
5. Deploy `order`, `vendor`, rồi `notification`, rồi `admin`, `frontend`.
6. Smoke: thanh toán một đơn thử → `/admin/notifications` thấy `order_paid` chuyển `sent`, người nhận đã che; ô "Delivery queue (Redis) unreachable" bằng 0. Duyệt một shop thử → `vendor_approved` `sent`. Reset mật khẩu từ UI tới mailbox thử nghiệm, đổi mật khẩu thành công.

Đơn hoặc shop đã được thông báo trước nâng cấp giữ nguyên bản ghi cũ (`source = legacy`) và không bị gửi lại.

## Theo dõi và cảnh báo

Mỗi phút log `notification_delivery_report` (warn khi có `parked`, chờ quá 15 phút hoặc Redis không truy cập được). Dashboard `/admin` có nhóm Notifications.

| Chỉ số | Ý nghĩa | Ngưỡng gợi ý |
|---|---|---|
| `parked` | Hết lượt thử vì lỗi tạm thời kéo dài | > 0: cảnh báo |
| `pending_over_15m`, `oldest_pending_seconds` | Hàng chờ cũ (provider chậm hoặc worker dừng) | > 0 trong 15 phút |
| `failed_24h` | Người nhận không tồn tại, tài khoản khóa, địa chỉ bị từ chối | tăng đột biến |
| `delivery_p95_seconds_24h` | Độ trễ từ lúc nhận tới lúc gửi | > 300 |
| `sent_24h` | Lưu lượng | giảm về 0 giờ cao điểm |
| `queue_unreachable` | Redis không trả lời: yêu cầu vẫn được ghi nhưng chưa gửi | = 1: cảnh báo |
| `queue_size`, `queue_retry`, `queue_archived` | Job trong Asynq (chờ/hẹn giờ/đang chạy/chạy lại); job bỏ cuộc vì lỗi database | `queue_retry` hoặc `queue_archived` tăng: kiểm tra database |

Log khác: `notification_accepted`, `notification_sent`, `notification_retry`, `notification_not_delivered`, `notification_finish_failed`, `notification_enqueue_failed`, `notification_jobs_requeued_from_postgres` (warn: job bị mất đã được đẩy lại), `notification_job_failed`, `notification_queue_unreachable`, `password_reset_delivery_failed` (chỉ có delivery id và lý do), `vendor_notice_pending` (Vendor). Không log nào chứa địa chỉ, nội dung hay token.

Reset mật khẩu thất bại sau 5 lần nằm ở Identity: `SELECT count(*) FROM password_reset_deliveries WHERE status = 'failed' AND created_at > now() - interval '1 day';` trên `identity_db`.

## Sự cố thường gặp

- **Provider/SMTP ngừng:** checkout, duyệt shop vẫn chạy; thông báo nằm `pending` và tự gửi lại (30 giây, gấp đôi mỗi lần, tối đa 1 giờ, 8 lần ≈ 4 giờ). Quá thời gian thì `parked`. Khi provider hoạt động lại: ở `/admin/notifications` lọc `parked`, "Retry" kèm lý do (thêm 3 lượt, có audit).
- **Redis ngừng hoặc mất dữ liệu:** checkout, duyệt shop và việc nhận yêu cầu vẫn chạy (202, ghi `pending`); chỉ việc gửi dừng. Dashboard báo "Delivery queue (Redis) unreachable". Khi Redis lên lại, mỗi 30 giây Notification đẩy lại job cho bản ghi trễ hơn 1 phút (log `notification_jobs_requeued_from_postgres`); không cần thao tác tay, không xóa gì trong PostgreSQL. Redis mất hẳn dữ liệu cũng được phục hồi như vậy.
- **Job Asynq bị archived** (`queue_archived` > 0, thường do database lỗi kéo dài): vòng khôi phục tự thay job khi bản ghi còn `pending`; không cần xóa tay.
- **Cần dừng gửi gấp** (gửi nhầm, provider khóa tài khoản): đặt `NOTIFICATION_DELIVERY_PAUSED=true` và khởi động lại Notification. Worker Asynq không chạy; yêu cầu vẫn được ghi và job vẫn nằm trong Redis. Không xóa hàng chờ (PostgreSQL hay Redis).
- **Bounce / người nhận bị từ chối (5xx):** `failed`, không tự gửi lại. Sửa email ở tài khoản người dùng rồi retry nếu cần.
- **Gửi chậm hoặc "may have been delivered":** mất kết nối sau khi đã chuyển nội dung cho relay; hệ thống gửi lại (at-least-once), người nhận có thể nhận 2 email. Không có exactly-once với SMTP.
- **Worker dừng giữa chừng:** lease hết hạn sau khoảng 50 giây thì vòng khôi phục đẩy job lượt kế và worker khác nhận; nếu đó là lần cuối thì `parked` với lý do "stopped during the last attempt".
- **Thông báo shop bị Notification từ chối:** Vendor đặt `parked_at` trong `vendor_notification_outbox` (ô "Shop decision notices Notification refused"). Kiểm tra `last_error`; sửa nguyên nhân rồi `UPDATE vendor_notification_outbox SET parked_at = NULL, attempts = 0, next_attempt_at = now() WHERE id = '<id>'` qua quy trình thay đổi có duyệt.

## Rollback

- Image Notification về bản cũ: vẫn gửi đồng bộ và ghi bản ghi (cột mới có default). Thông báo `pending`/`parked` nằm lại trong bảng và được gửi khi lên lại bản mới (vòng khôi phục đẩy job từ PostgreSQL). Job Asynq còn trong Redis không ảnh hưởng bản cũ. Producer mới vẫn chạy với bản cũ.
- Cấu hình Redis (AOF, `noeviction`) không cần rollback; nếu bỏ, chỉ mất khả năng giữ job qua restart, job vẫn được khôi phục từ PostgreSQL.
- Không chạy migration down khi còn thông báo `pending`/`sending`/`parked` hoặc đã có audit, hoặc Vendor còn notice chưa gửi: các migration down từ chối trong trường hợp đó. Không xóa hàng chờ để "làm sạch".
