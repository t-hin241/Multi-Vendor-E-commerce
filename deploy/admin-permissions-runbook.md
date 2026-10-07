# Quyền admin theo bundle và phê duyệt hai người (AF-19)

Đặc tả: `docs/modular/add_features/19-admin-permissions-approvals.md`. Role `admin` vẫn bắt buộc, nhưng khi bật cờ ở Identity thì mỗi route admin còn cần một **bundle** được cấp rõ ràng (deny mặc định). Thao tác tiền thủ công ở Payment cần **hai admin khác nhau**, mỗi người xác nhận lại mật khẩu. Đây là xác nhận lại mật khẩu, **không phải MFA**.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Identity | Migration `000005_admin_permissions`: cột `users.permission_version`, bảng `admin_permission_grants`, `reauth_proofs` (chỉ lưu hash, 5 phút, dùng một lần). Không admin nào tự nhận bundle khi migrate |
| Identity API | `GET /api/auth/permissions` (bundle của mình), `POST /api/auth/reauthentications {password, purpose, operation_hash}` → proof; `GET /api/auth/admin/permission-subjects`, `GET/POST /api/auth/admin/permission-grants`, `DELETE /api/auth/admin/permission-grants/:id` (cần `access.manage`, lý do, `expected_version`; không tự đổi quyền mình → 409 `self_approval`) |
| Identity nội bộ | `POST /internal/admin-permissions/check`, `POST /internal/reauth-proofs/consume` |
| Mọi service có route admin | Bảng `transport.AdminRoutes` (method + route → bundle) và `adminaccess.Guard` sau `RequireRole("admin")`. Route không có trong bảng bị từ chối. Test `TestEveryAdminRouteNamesAPermission` fail nếu thêm route admin mà quên bảng |
| Payment | Migration `000012_admin_approvals`: `approval_requests` (nội dung bất biến, một yêu cầu mở mỗi mục tiêu, `execution_ref` unique). Route `/api/payments/admin/approval-requests` (+ `/:id/submission`, `/:id/decisions`, `/:id/cancellation`) |
| Vendor | Duyệt/từ chối đích nhận tiền và xem chi tiết đầy đủ cần `finance.approve` và (khi bật cờ) proof bound vào `payout_account:<id>:v<version>:verify|reject|details` |
| Order, Payment, Shipment | Người được giao case hỗ trợ / work item SLA và người nhận nhắc SLA phải có `support.manage` |
| Frontend | Menu admin lọc theo bundle; `/admin/access` (cấp/thu hồi); `/admin/approvals` (hàng chờ); hộp thoại xác nhận mật khẩu; các màn refund/payout/điều chỉnh sổ tự tạo nháp yêu cầu khi Payment trả `approval_required` |

## Bundle

| Bundle | Dùng cho |
|---|---|
| `support.manage` | Đơn, hỗ trợ, trả hàng (quyết định/nhận), vận đơn admin, work item SLA, thông báo |
| `moderation.manage` | Duyệt shop, sản phẩm, đánh giá, yêu cầu nhập kho, khóa/mở tài khoản |
| `finance.read` | Xem refund, đối soát, số dư, lô payout, hoa hồng, yêu cầu phê duyệt, danh sách tài khoản nhận tiền |
| `finance.prepare` | Tạo yêu cầu refund từ đơn, ghi kết quả refund/payout, điều chỉnh sổ, tạo lô payout, retry đồng bộ, tạo/gửi yêu cầu phê duyệt |
| `finance.approve` | Duyệt yêu cầu phê duyệt; xác minh đích nhận tiền và đọc chi tiết đầy đủ |
| `platform.configure` | Danh mục, thuộc tính, chính sách, vận chuyển, lý do kiểm duyệt, đặt hoa hồng, replay sự kiện/effect |
| `analytics.read` | Dashboard admin |
| `audit.read` | Tra cứu audit (chỉ xem, không replay) |
| `access.manage` | Cấp/thu hồi bundle, thu hồi phiên đăng nhập |

Một người có nhiều bundle vẫn **không tự duyệt** yêu cầu mình tạo. Bảng chi tiết route → bundle nằm trong `admin_permissions.go` của từng service.

## Cờ

| Biến compose | Service nhận (`FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED`) | Bật thì |
|---|---|---|
| `IDENTITY_FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED` | Identity | Admin chỉ có các bundle được cấp. Tắt: mọi admin active có mọi bundle (như trước AF-19) |
| `PAYMENT_FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED` | Payment | Ghi kết quả refund/payout và điều chỉnh sổ trực tiếp trả 409 `approval_required`; chỉ đi qua yêu cầu phê duyệt |
| `VENDOR_FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED` | Vendor | Quyết định/đọc chi tiết đích nhận tiền cần proof |

## Luồng phê duyệt (Payment)

1. Người chuẩn bị (`finance.prepare`) bấm thao tác như cũ → Payment trả `approval_required` → giao diện tạo **nháp** với đúng dữ liệu (hoặc `POST /approval-requests {operation_kind, target_id, payload, reason}`). Payment lưu snapshot của mục tiêu và `payload_hash`.
2. Người chuẩn bị xác nhận mật khẩu (purpose `payment.approval.submit`, operation = `payload_hash`) → `submission` → `pending`, hạn 24 giờ.
3. Admin khác có `finance.approve` xác nhận mật khẩu (purpose `payment.approval.decide`) → `decisions {approve|reject, reason, expected_version}`. Khi duyệt: kiểm tra người chuẩn bị vẫn còn `finance.prepare`, khóa mục tiêu, so snapshot (khác → 409 `stale_snapshot`, không thực thi), thực thi và ghi `approved` + `execution_ref` + audit **trong cùng một transaction**. Không gọi provider.
4. Hết 24 giờ → `expired` (cần tạo yêu cầu mới). Hai người duyệt cùng lúc: chỉ một thành công (khóa dòng + version).

Lỗi: 403 `missing_permission`, 403 `reauthentication_required` (proof thiếu/sai/đã dùng/hết hạn), 409 `self_approval`, `stale_snapshot`, `stale_request`, `approval_expired`, `approval_open`, `maker_permission_revoked`; 503 `auth_unavailable` khi không hỏi được Identity (mọi route admin bị từ chối, không mở rộng quyền).

## Rollout

1. Backup `identity_db`, `payment_db`. Chạy migration Identity `000005`, Payment `000012` khi các cờ đang tắt. Deploy Identity trước (có endpoint `/internal/admin-permissions/check`), sau đó các service khác và frontend. Khi cờ Identity tắt, guard vẫn hỏi Identity và mọi admin vẫn qua — kiểm hành vi không đổi, theo dõi `admin_route_unmapped` (phải bằng 0) và `auth_unavailable`.
2. Bootstrap người quản lý quyền đầu tiên (một lần, có audit, từ chối nếu đã có người giữ `access.manage`; tài khoản phải là admin active có sẵn):

   ```
   docker compose run --rm --no-deps --entrypoint /usr/local/bin/bootstrap-access \
     -e IDENTITY_BOOTSTRAP_CONFIRM=true -e IDENTITY_BOOTSTRAP_ACCESS_EMAIL \
     -e IDENTITY_BOOTSTRAP_OPERATOR -e IDENTITY_BOOTSTRAP_REASON identity
   ```

   Giá trị đặt qua secret manager/biến môi trường của phiên vận hành, không ghi vào file commit.
3. Người quản lý quyền cấp bundle cho **từng** operator ở `/admin/access` theo bảng phân công đã duyệt (ghi trong release record). Không cấp `finance.prepare` và `finance.approve` cho cùng người nếu có thể; dù có cũng không tự duyệt được.
4. Bật cờ Identity. Kiểm từng operator mở được đúng màn hình. Có sự cố: tắt cờ Identity (mọi admin trở lại đủ quyền như trước); không cần rollback image.
5. Bật cờ Vendor, rồi cờ Payment khi đã có ít nhất hai người `finance.approve` khác người chuẩn bị. Diễn tập một refund và một payout qua phê duyệt ở staging.

Rollback Payment: tắt cờ → thao tác trực tiếp trở lại (vẫn audit); yêu cầu đang mở giữ nguyên, có thể huỷ. Không chạy down `000012`/`000005` khi đã có dữ liệu (down tự từ chối). **Không** mở rộng quyền cho thao tác mới khi rollback: nếu Identity lỗi, route admin trả 503 thay vì cho qua.

## Theo dõi

Log có cấu trúc: `admin_permission_denied` (route, permission, actor), `admin_route_unmapped`, `reauth_proof_refused`, `payment_approval_decided`. Audit: Identity `permission_granted`, `permission_revoked`, `access_bootstrap`, `reauthenticated`; Payment `approval_drafted/submitted/approved/rejected/expired/cancelled`. Theo dõi yêu cầu `pending` gần hết hạn và việc dùng bootstrap.

## Kiểm thử

Unit: `go test ./pkg/adminaccess/... ./services/*/internal/transport/` (bảng phủ mọi route admin). Integration (DB tạm, tên kết thúc `_test`; Identity cần đúng `identity_test`): Identity grant/revoke/version race/bootstrap/proof một lần; Payment `TestManualMoneyActionsNeedTwoAdmins`. Frontend: `npx vitest run src/lib/admin-access.test.ts`.
