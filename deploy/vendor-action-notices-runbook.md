# Thông báo công việc cho shop (AF-08)

Đặc tả: `docs/modular/add_features/08-vendor-action-notices.md`.

Shop nhận email khi có việc cần làm:

| Việc | Nguồn | Khi nào | Nhóm |
|---|---|---|---|
| Đơn mới cần chuẩn bị | Order | Gói hàng đã được xác minh thanh toán **và** đã trừ kho (cùng transaction với trạng thái `paid`). Thanh toán bị từ chối vì hết giữ hàng thì không gửi | `orders` |
| Người mua yêu cầu hủy gói | Order (AF-03) | Yêu cầu hủy gói đã thanh toán do buyer hoặc admin mở. Shop tự mở thì không gửi cho chính shop | `orders` |
| Yêu cầu trả hàng mới | Order | Buyer gửi yêu cầu trả hàng | `returns` |
| Hàng trả đang gửi về | Order (AF-05) | Buyer báo đã gửi kiện trả | `returns` |
| Kết quả payout | Payment | Admin ghi một khoản payout là chuyển thành công hoặc thất bại | `finance` |

Người nhận:

- **Chủ shop** luôn nhận mọi nhóm. Đây là email giao dịch bắt buộc, không phải marketing.
- **Nhân viên** (AF-17) nhận một nhóm chỉ khi thỏa đủ hai điều kiện:
  - đang có quyền tương ứng: `orders.fulfill`, `returns.handle` hoặc `finance.read`;
  - đã tự chọn nhóm đó ở thẻ "Email công việc của shop" trên trang tổng quan `/vendor`.
- Một người có nhiều vai trò chỉ nhận một email cho mỗi việc.
- Người bị thu hồi quyền, tài khoản bị khóa, hoặc nhân viên khi cờ AF-17 đang tắt đều không nhận.

Email chỉ chứa mã gói hàng, mã yêu cầu hoặc mã payout, kèm đường dẫn vào trang quản lý (`/vendor/orders`, `/vendor`, `/vendor/payout-accounts`). Email không chứa địa chỉ người mua, số tiền hay số tài khoản. Đường dẫn không phải credential: mở ra vẫn phải đăng nhập và có quyền.

## Luồng

1. Producer ghi sự việc cùng transaction với thay đổi nghiệp vụ:
   - **Order:** effect `notify_vendor`, target `<loại việc>:<mã>`, nên mỗi việc chỉ được ghi một lần.
   - **Payment:** bảng `payment_vendor_notices`, unique theo payout item và kết quả.
2. Relay gửi event `order.vendor_action_required` hoặc `payment.vendor_action_required`. Event id là id của effect hoặc của dòng outbox.
3. Consumer `notification-vendor-actions` ghi `vendor_action_events`, unique theo (source, event_id). Bước này không gọi service khác.
4. Worker của Notification (mỗi 5 giây, hoặc ngay khi có event mới) xử lý từng event:
   - gọi Vendor `GET /internal/vendors/:id/notification-recipients?purpose=orders|returns|finance`;
   - lọc nhân viên theo lựa chọn của họ;
   - ghi một notification cho mỗi người **và** đánh dấu event `resolved`, kèm danh sách người nhận và `permission_version`, trong cùng một transaction. Worker dừng giữa chừng thì lease hết hạn và worker khác làm lại; kết quả trễ của worker cũ bị từ chối, nên không có danh sách thứ hai.
5. Gửi email theo hàng đợi Asynq như mọi thông báo khác: retry, park, admin retry (xem [notification-runbook](notification-runbook.md)). SMTP vẫn là at-least-once: lỗi không rõ kết quả có thể làm một email tới hai lần.

## Trạng thái event (`vendor_action_events.status`)

| Giá trị | Ý nghĩa | Xử lý |
|---|---|---|
| `pending` / `resolving` | Chờ xác định người nhận | Tự động. Vendor lỗi thì thử lại với backoff 30 giây tăng dần |
| `resolved` | Đã ghi email cho từng người | Theo dõi ở danh sách email phía trên trang |
| `no_recipient` | Không ai được phép nhận: chủ shop bị khóa, shop không còn, hoặc không có nhân viên đủ điều kiện | Admin liên hệ shop. Khi tài khoản chủ shop mở lại, bấm Retry (cần lý do, có audit) |
| `parked` | Vendor không trả lời sau 8 lần (khoảng 1,5 giờ) | Kiểm Vendor, rồi bấm Retry |

Admin xem ở `/admin/notifications`, mục "Shop work notices" (quyền `support.manage`). Cột Recipients chỉ hiện số người nhận, không hiện họ là ai.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| `pkg/events` | `order.vendor_action_required`: `vendor_id`, `vendor_order_id`, `order_id`, `action_kind`, `reference_id`.<br>`payment.vendor_action_required`: `vendor_id`, `payout_id`, `outcome`.<br>NATS: durable `notification-vendor-actions` |
| Vendor | `GET /internal/vendors/:vendorId/notification-recipients?purpose=` (chỉ Notification gọi được). Trả chủ shop và nhân viên có quyền tương ứng, bỏ người không còn được phép thao tác. Identity lỗi thì trả 503, không trả danh sách thiếu người. Không có migration |
| Notification migration `000005_vendor_action_notices` | Bảng `vendor_action_events`, `notification_preferences`; audit `vendor_action_event`. Down tự từ chối khi có dữ liệu |
| Notification API | **Nội bộ** (Order, Payment; dùng khi `EVENT_PUBLISHING=http`): `POST /internal/vendor-action-notices`.<br>**Người dùng đã đăng nhập:** `GET/PATCH /api/notifications/preferences {optional_vendor_categories, expected_version}`; sai version trả 409.<br>**Admin:** `GET /api/notifications/admin/vendor-actions?status=`, `GET …/summary`, `POST …/:id/retry {reason}` |
| Order migration `000024_vendor_action_notices` | Thêm effect `notify_vendor`. Down tự từ chối khi đã có effect loại này |
| Payment migration `000015_vendor_notices` | Bảng `payment_vendor_notices`. Ghi khi admin (hoặc yêu cầu duyệt AF-19) ghi kết quả payout. Down từ chối khi còn dòng chưa gửi |
| Frontend | **Shop:** thẻ "Email công việc của shop" trên `/vendor`.<br>**Admin:** mục "Shop work notices" trên `/admin/notifications` |

## Cờ

| Biến | Service | Mặc định | Tắt thì |
|---|---|---|---|
| `FEATURE_VENDOR_ACTION_NOTICES_ENABLED` (compose `NOTIFICATION_FEATURE_VENDOR_ACTION_NOTICES_ENABLED`) + `VENDOR_SERVICE_URL` | Notification | `false` | Không chạy consumer: event nằm lại trong stream, không mất. Route nội bộ trả 404, effect HTTP của Order park lại. Trang tùy chọn ẩn đi. Email đã ghi vẫn được gửi |
| `FEATURE_VENDOR_ACTION_NOTICES_ENABLED` (compose `ORDER_FEATURE_VENDOR_ACTION_NOTICES_ENABLED`) | Order | `false` | Không ghi việc mới cho shop. Effect đã ghi vẫn gửi đi |
| `FEATURE_VENDOR_ACTION_NOTICES_ENABLED` (compose `PAYMENT_FEATURE_VENDOR_ACTION_NOTICES_ENABLED`) | Payment | `false` | Không ghi kết quả payout mới, và relay ngừng. Dòng chưa gửi nằm lại cho tới khi bật lại |

Payment chỉ gửi qua event bus. Với `EVENT_PUBLISHING=http`, dòng nằm chờ và log báo `payment_vendor_notices_wait_for_event_bus`.

## Rollout

Consumer bật trước producer.

1. Backup `notification_db`, `order_db`, `payment_db`.
2. Deploy Vendor (endpoint mới), rồi Notification (`000005`). Bật cờ Notification. Kiểm quyền NATS có durable `notification-vendor-actions`.
3. Deploy Order (`000024`) và Payment (`000015`) với cờ tắt. Kiểm các luồng cũ không đổi.
4. Ở staging, bật cờ Order và Payment rồi thử:
   - đơn hai shop thanh toán → mỗi chủ shop nhận đúng một email;
   - giữ hàng hết hạn trước khi thanh toán → không có email;
   - nhân viên có `orders.fulfill` nhưng chưa chọn nhóm → không nhận; chọn rồi → nhận; bị thu hồi quyền → việc mới không tới nữa;
   - khóa tài khoản chủ shop → event `no_recipient` → mở khóa → Retry;
   - payout thất bại → email không chứa số tiền hay số tài khoản;
   - tắt SMTP giữa chừng rồi restart → email vẫn tới (có thể trùng nếu SMTP không rõ kết quả).
5. Chỉ nghiệm thu với SMTP staging và hộp thư thật; sender `mock` không tính.

Không backfill tự động. Đơn đã thanh toán trước khi bật cờ không gửi email; shop xem trong `/vendor/orders` như trước. Xem PW trong `99-pending-work.md`.

## Theo dõi

- Log Notification:
  - `vendor_action_recorded`, `vendor_action_resolved`, `vendor_action_retry`;
  - `vendor_action_needs_review` (error);
  - `vendor_action_report` mỗi phút; warn khi có `no_recipient`, `parked`, hoặc event chờ quá 15 phút.
- Log Vendor: `shop_notice_recipients_unavailable` (Identity hoặc Vendor lỗi).
- Log Payment: `payment_vendor_notice_relay_failed`, `payment_vendor_notices_need_review` (relay hỏng 20 lần).
- Email trùng theo việc: `SELECT source, event_id, user_id, count(*) FROM notifications WHERE type LIKE 'vendor_%' GROUP BY 1,2,3 HAVING count(*) > 1` phải rỗng (dedup key).

## Rollback

1. Tắt cờ Order và Payment: không ghi việc mới; việc đã ghi vẫn gửi xong.
2. Nếu cần dừng gửi: tắt cờ Notification. Event nằm lại trong stream. Email đã ghi vẫn được gửi, trừ khi bật `NOTIFICATION_DELIVERY_PAUSED`.
3. Không chạy migration down khi đã có dữ liệu (các down tự từ chối).
