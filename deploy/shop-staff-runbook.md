# Nhân viên và phân quyền theo shop (AF-17)

Đặc tả: `docs/modular/add_features/17-shop-staff-permissions.md`. Vendor sở hữu membership, quyền và lời mời; Identity vẫn là nguồn sự thật cho tài khoản. Không có role toàn cục mới: một tài khoản buyer có thể là nhân viên shop mà vẫn giữ role buyer. Chỉ tài khoản vendor mới tạo được shop.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Vendor | Migration `000011_shop_staff`: `vendor_memberships`, `membership_permissions`, `staff_invitations`, `membership_audit_logs` (append-only). Backfill chủ shop từ `vendors.user_id`; trigger tạo dòng owner cho mọi shop mới (kể cả do image cũ tạo sau rollback) và chặn đổi chủ shop |
| Vendor API | `GET /api/vendor/accessible-shops`, `GET /api/vendor/staff-permissions`, `POST /api/vendor/:vendorId/staff-invitations`, `GET` + `DELETE .../staff-invitations/:id`, `POST /api/vendor/staff-invitations/accept`, `GET/PATCH/DELETE /api/vendor/:vendorId/members[/:userID]`. `GET /api/vendor/:vendorId` mở cho mọi thành viên, trả kèm `role`, `capabilities` |
| Contract nội bộ | `POST /internal/vendors/authorize {actor_user_id, vendor_id, permission}` → `allowed`, `status`, `role`, `membership_version`. Chỉ cho catalog, inventory, order, review, shipment, payment. Câu hỏi hợp lệ luôn trả 200; 400 khi sai dạng; 503 `authorization_unavailable` khi không xác định được |
| Catalog, Inventory, Order, Shipment, Review | Mọi điểm kiểm chủ shop chuyển sang `pkg/shopaccess` với quyền cụ thể; route người bán dùng `middleware.SellerConsole()` (buyer hoặc vendor, hành động với tư cách shop). Không cache kết quả dương qua request |
| Notification | `POST /internal/notifications/staff-invitations` (chỉ Vendor) gửi email ngay, không lưu |
| Gateway | Rate rule `staff`: 20 POST/phút/IP cho `/api/vendor/.../staff-invitations` |
| Frontend | Console người bán đọc `accessible-shops`, lọc menu theo quyền; trang `/vendor/staff`; trang nhận lời mời `/staff-invitations/accept` |

`GET /internal/vendors/:vendorId/owned-by/:userID` giữ nguyên, chỉ trả lời cho chủ shop. Client mới gặp Vendor cũ (404 ở `/authorize`) sẽ lùi về endpoint này, nên không bao giờ cấp quyền cho staff khi chưa đủ phiên bản.

## Quyền (registry v1)

| Quyền | Dùng cho |
|---|---|
| `products.read` / `products.write` | Xem / tạo, sửa, ảnh, biến thể, gửi duyệt, bật tắt sản phẩm; xem đánh giá |
| `inventory.read` / `inventory.adjust` | Xem tồn kho, yêu cầu nhập / tạo mục kho, yêu cầu nhập thêm, kiểm kê |
| `orders.read` / `orders.fulfill` | Xem đơn, xuất CSV, vận chuyển, phương thức vận chuyển / chuẩn bị đơn, tạo và cập nhật vận đơn |
| `returns.handle` | Danh sách, xác nhận, nhận hàng trả |
| `support.reply` | Hồ sơ hỗ trợ của shop; trả lời và báo cáo đánh giá |
| `analytics.read` | Tổng quan bán hàng (`/dashboard`, tóm tắt đơn) |
| `finance.read` | Phần thanh toán của tổng quan; thiếu quyền này thì `payments_restricted=true` |
| `staff.manage` | Mời, sửa quyền, thu hồi nhân viên trong phạm vi quyền mình đang có |
| `shop.availability.write`, `marketing.manage`, `live.host` | Có trong registry nhưng **chưa cấp được** (`available=false`) vì tính năng chưa tồn tại |
| `payout_destination.write`, `shop.settings.write` | Chỉ chủ shop, không bao giờ cấp cho staff ở v1 (tài khoản nhận tiền, phương thức vận chuyển). Hồ sơ shop, logo, địa chỉ, chính sách shop cũng vẫn chỉ chủ shop |

Quy tắc: không ai sửa quyền của chính mình; staff có `staff.manage` chỉ cấp lại quyền mình có và chỉ đụng tới thành viên có quyền nằm trong tập của mình; chủ shop không bị xóa/sửa (409 `last_owner`); PATCH/DELETE cần `expected_version` (409 `version_conflict`). Staff tự rời shop được.

## Cấu hình

| Biến (Vendor) | Mặc định | Ý nghĩa |
|---|---|---|
| `FEATURE_SHOP_STAFF_ENABLED` | `false` | Tắt: chỉ chủ shop qua được, không tạo/nhận lời mời; chủ shop vẫn thu hồi được staff |
| `SHOP_STAFF_INVITES_PAUSED` | `false` | Dừng lời mời mới và việc thêm quyền; staff hiện có vẫn làm việc, vẫn bớt/thu hồi được |
| `STAFF_INVITATION_FINGERPRINT_KEY` | — | Bắt buộc khi bật: bí mật ≥ 32 ký tự, khác mọi service key. Khóa HMAC lưu thay cho email được mời. Đổi khóa làm lời mời đang chờ không nhận được nữa |
| `STAFF_INVITATION_ACCEPT_URL` | — | Bắt buộc khi bật: trang `/staff-invitations/accept` của frontend; HTTPS ở staging/production, không có query/fragment (token được thêm vào `#token=`) |

Email mời dùng SMTP của Notification (`SMTP_*`). Giá trị bí mật chỉ đặt qua cấu hình triển khai, không ghi vào file commit.

## Lời mời và email

- Lời mời có hạn 7 ngày, một lần. Chỉ lưu `email_hint` (dạng `a***@domain`) và HMAC của địa chỉ; địa chỉ thật chỉ giữ tới khi gửi xong rồi bị xóa. Mời lại cùng địa chỉ thay lời mời cũ (`superseded`).
- Worker trong Vendor (mỗi 2 giây, lease 1 phút, `FOR UPDATE SKIP LOCKED`) tạo token mới cho mỗi lần gửi và chỉ lưu hash. Lỗi gửi thử lại với backoff, sau 6 lần thì `parked` (hiện ở danh sách lời mời). Nếu Notification gửi được nhưng trả lời bị mất, lần thử sau gửi email mới và link cũ hết hiệu lực.
- Nhận lời mời: người nhận đăng nhập bằng đúng email được mời, mở link, bấm "Chấp nhận". Token chỉ đi trong body POST. Link bị chuyển tiếp cho tài khoản khác → 403; dùng lại → 409 `invitation_used`; đã thay/thu hồi → 409 `invitation_revoked`; hết hạn → 409 `invitation_expired`. Người mời bị thu hồi `staff.manage` thì lời mời họ gửi không nhận được nữa.
- Identity hiện **chưa xác minh email khi đăng ký**. Việc giữ hộp thư được chứng minh bằng token gửi tới địa chỉ đó cộng với việc tài khoản đăng nhập có đúng địa chỉ đó.

## Revoke và thao tác đang chạy

Mỗi request hỏi lại Vendor, nên thu hồi/bớt quyền chặn ngay request kế tiếp. Request đã qua bước kiểm quyền trước lúc thu hồi có thể hoàn tất (không hủy hành động đã bắt đầu). Tài khoản bị khóa ở Identity bị từ chối ngay (Vendor đọc trạng thái tài khoản mỗi lần authorize). Hiện chưa có job nền nào chạy thay người bán; khi thêm, job phải authorize lại bằng `requester_id`, không dùng service key để vượt quyền.

## Rollout

1. Backup `vendor_db`. Chạy migration `000011` (preflight dừng nếu có shop không có chủ). Kiểm tra: `SELECT count(*) FROM vendors v LEFT JOIN vendor_memberships m ON m.vendor_id=v.id AND m.role='owner' WHERE m.vendor_id IS NULL` phải bằng 0.
2. Deploy Vendor (có `/authorize`) với cờ tắt. Sau đó deploy Catalog, Inventory, Order, Shipment, Review, Notification, Gateway, Frontend. Thứ tự ngược vẫn an toàn (client lùi về owner-only) nhưng staff chưa dùng được.
3. Kiểm owner-only parity khi cờ tắt: chủ shop thao tác sản phẩm, kho, đơn, vận chuyển, trả hàng, hỗ trợ, đánh giá như trước; theo dõi log `shop_permission_denied` và `shop_authorization_unavailable`.
4. Cấu hình key + URL, bật `FEATURE_SHOP_STAFF_ENABLED` cho shop pilot (cờ áp dụng toàn hệ thống). Thử: mời một tài khoản buyer, nhận lời mời, kiểm từng quyền, thu hồi và xác nhận request kế tiếp bị chặn.

Theo dõi: tỉ lệ `shop_permission_denied` theo caller, `shop_authorization_unavailable` (Vendor/Identity lỗi → mọi thao tác người bán 503), lời mời `parked`, thay đổi quyền trong tra cứu audit admin (entity `shop_membership`), membership mồ côi (staff active của shop đã bị khóa lâu).

Rollback: đặt `SHOP_STAFF_INVITES_PAUSED=true` để dừng cấp mới; nếu phải tắt hẳn quyền của staff, đặt `FEATURE_SHOP_STAFF_ENABLED=false` và **thông báo trước cho các shop** (staff mất truy cập ngay, chủ shop xử lý tiếp). Image cũ của các service vẫn chạy được vì `owned-by` không đổi. Không chạy down `000011` khi đã có staff, lời mời hoặc audit (down tự từ chối).

## Kiểm thử

- Unit: `go test ./pkg/shopaccess/ ./services/vendor/... ./services/notification/... ./gateway/...` và các service đã chuyển.
- Integration: `VENDOR_TEST_DATABASE_URL` trỏ tới PostgreSQL tạm có tên DB kết thúc bằng `_test`; test tạo schema riêng (trigger owner, mời → nhận → authorize → thu hồi, nhận đồng thời một token, audit append-only, down migration từ chối).
- Frontend: `npx vitest run src/lib/shop-access.test.ts`, `tsc --noEmit`, ESLint.

## Email công việc (AF-08)

Vendor trả danh sách người nhận email công việc qua `GET /internal/vendors/:vendorId/notification-recipients?purpose=orders|returns|finance` (chỉ Notification gọi được). Danh sách gồm chủ shop và nhân viên đang có `orders.fulfill`, `returns.handle` hoặc `finance.read`; nhân viên chỉ có mặt khi cờ shop staff đang bật. Nhân viên còn phải tự chọn nhóm việc ở `/vendor` thì mới nhận. Thu hồi quyền có hiệu lực ngay với việc mới. Chi tiết: `deploy/vendor-action-notices-runbook.md`.

## Phiên bản quyền trong audit (PW-021)

Quyền được kiểm khi request bắt đầu, còn việc thu hồi có thể xảy ra trước khi transaction của lệnh commit. Không gọi lại Vendor trong transaction (quy tắc: không giữ transaction trong lúc gọi service khác). Thay vào đó, service sở hữu ghi `membership_version` của grant đã cho phép lệnh vào audit:

| Service (migration) | Bảng | Lệnh |
|---|---|---|
| Catalog (`000016`) | `product_audit_logs` | Sửa nội dung/giá sản phẩm |
| Inventory (`000008`) | `stock_movements`, `inventory_operation_audit` | Tạo tồn ban đầu, kiểm kê, thao tác kho |
| Order (`000026`) | `return_request_events` | Xác nhận/nhận hàng trả |
| Shipment (`000011`) | `shipment_tracking_events` | Bàn giao, cập nhật giao hàng |

`NULL` nghĩa là lệnh không đi qua quyền shop (buyer, admin, hệ thống).

Điều tra một thao tác nghi vấn:

1. Lấy `membership_version` trong audit.
2. So với `membership_audit_logs` của Vendor (`staff_revoked` / `staff_permissions_changed` có version mới hơn).
3. Nếu version đã bị thu hồi trước thời điểm ghi, đó là thao tác lọt khe; xử lý theo quy trình sự cố.

Down của các migration này tự từ chối khi đã có giá trị được ghi.
