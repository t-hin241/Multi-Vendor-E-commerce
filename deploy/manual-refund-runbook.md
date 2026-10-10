# Hoàn tiền chuyển khoản thủ công (AF-06)

Đặc tả: `docs/modular/add_features/06-manual-refund-operations.md`.

Khi bật, refund không còn ghi "đã hoàn" bằng một thao tác. Phải đi đủ các bước:

1. Buyer nhập tài khoản nhận.
2. Nhân sự tài chính xác minh tài khoản.
3. Một operator nhận việc và chuyển khoản ngoài ứng dụng.
4. Operator ghi mã giao dịch ngân hàng.
5. Một người duyệt xác nhận.

Chỉ bước xác nhận mới chuyển refund sang `succeeded`. Bước này ghi sổ đối soát, outbox báo Order và audit trong cùng một transaction. Payment không gọi ngân hàng.

## Thành phần

| Nơi | Thay đổi |
|---|---|
| Payment migration `000013_manual_refunds` | Ba bảng mới:<br>• `refund_destinations`: mỗi lần buyer đổi tài khoản là một phiên bản mới. Số tài khoản và tên chỉ lưu dạng mã hoá AES-256-GCM, AAD `refund_destination:<refund>:v<version>`. Trong DB chỉ có mã ngân hàng và 4 số cuối ở dạng rõ.<br>• `manual_refund_attempts`: mỗi refund chỉ có một lần chuyển đang mở (`ready`/`executing`/`submitted`/`unknown`). Mã giao dịch là duy nhất theo tài khoản nguồn.<br>• `refund_evidence`: chứng từ chuyển khoản.<br>Trigger chặn xoá và chặn sửa ciphertext. Down tự từ chối khi đã có dữ liệu |
| API buyer | `GET /api/payments/refunds?order_id=`, `GET /api/payments/refunds/:id`: trạng thái kèm tài khoản đã che.<br>`POST /api/payments/refunds/:id/beneficiary {bank_code, account_number, account_name, expected_version}` |
| API admin `/api/payments/admin` | `GET refunds/:id/manual` (`finance.read`)<br>`POST refunds/:id/destination-decisions` (`finance.approve`)<br>`POST refunds/:id/sensitive-access` (`finance.prepare`; chỉ người đang giữ lần chuyển, những người khác cần thêm `finance.approve`)<br>`POST refunds/:id/manual-attempts`, `refund-attempts/:id/claims`, `/cancellation`, `/evidence`, `/submissions` (`finance.prepare`)<br>`POST refund-attempts/:id/decisions` (`finance.approve`)<br>`GET refund-evidence/:id` (`finance.read`) |
| Gateway | Rate rule `refund_destination`: 10 lần/phút mỗi IP cho `POST …/beneficiary` |
| Frontend | Trang đơn của buyer có khối "Hoàn tiền": trạng thái, mốc thời gian và form nhập tài khoản.<br>`/admin/refunds` có nút "Bank transfer" mở các bước xử lý |

## Trạng thái

- Tài khoản nhận: `pending_verification` → `verified` hoặc `rejected`. Khi buyer gửi tài khoản mới, phiên bản cũ thành `superseded`.
- Lần chuyển:
  - `ready` → `executing` (có hạn giữ `MANUAL_REFUND_CLAIM_LEASE_MINUTES`) → `submitted` → `confirmed` hoặc `failed`.
  - `ready` có thể bị huỷ thành `voided`.
  - `executing` có thể thành `voided` nếu người giữ tự trả việc và cam kết chưa chuyển.
  - **Hết hạn giữ mà chưa ghi mã → `unknown`.** Không ai được tạo lần chuyển khác cho tới khi đối chiếu sao kê: nếu tiền đã đi thì ghi mã (`submitted`), nếu không thấy thì đánh dấu `failed`.
- Buyer đổi tài khoản khi lần chuyển còn `ready`: lần chuyển đó tự huỷ. Khi lần chuyển đã `executing`/`submitted`/`unknown`: không cho đổi tài khoản (409 `attempt_active`).
- Lỗi:
  - 409: `attempt_active`, `destination_changed`, `destination_not_verified`, `stale_attempt`, `lease_expired`, `self_approval`, `duplicate_bank_reference`, `manual_workflow_required`.
  - 403: `not_claim_owner`, `reauthentication_required`, `missing_permission`.
  - 503: `destination_key_unavailable`, `evidence_storage_unavailable`.

## Cờ và cấu hình (Payment)

| Biến | Ý nghĩa |
|---|---|
| `FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED` | Bật: buyer nhập được tài khoản, tạo được lần chuyển mới. Ghi `succeeded` một bước (trực tiếp hoặc qua phê duyệt AF-19) trả 409 `manual_workflow_required`; ghi `failed` một bước vẫn được khi không có lần chuyển mở |
| `REFUND_DESTINATION_KEY`, `REFUND_DESTINATION_KEY_VERSION` | Khoá base64 32 byte (`openssl rand -base64 32`) và số phiên bản. Bắt buộc khi bật cờ. Đặt qua secret của môi trường, không ghi vào file commit |
| `REFUND_DESTINATION_PREVIOUS_KEYS` | `version:base64,...`: các khoá cũ vẫn dùng để đọc dữ liệu cũ khi xoay khoá |
| `MANUAL_REFUND_CLAIM_LEASE_MINUTES` | Thời gian giữ một lần chuyển, mặc định 30 (5–240) |
| `REFUND_EVIDENCE_STORAGE_*`, `REFUND_EVIDENCE_BUCKET` | Bucket riêng tư cho chứng từ, không có bucket policy; compose dùng `refund-evidence` trên MinIO. Để trống endpoint thì không tải chứng từ lên được |
| `FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED` (AF-19) | Bật: các bước xác minh tài khoản, xem tài khoản đầy đủ và xác nhận/đánh dấu thất bại cần xác nhận lại mật khẩu; người xác nhận phải khác người nhận việc và người ghi mã. Tắt (pilot một người): người thực hiện tự xác nhận được, audit ghi `single operator:`. Trước khi mở phải có người kiểm tra ngoài hệ thống (AF-06 §4) |

## Rollout

1. Backup `payment_db`, sau đó chạy migration `000013` khi cờ đang tắt.
2. Deploy Payment trước, rồi gateway và frontend. Khi cờ tắt, buyer không thấy form, admin vẫn ghi kết quả refund như cũ.
3. Tạo khoá và đặt `REFUND_DESTINATION_KEY` bằng secret. Kiểm Payment khởi động không có log `refund_destination_key_missing`.
4. Chuẩn bị điều kiện vận hành và ghi vào release record:
   - tài khoản nguồn của sàn (tên dùng cho `source_account`);
   - quy trình đối chiếu sao kê;
   - người xác minh tài khoản, người chuyển và người duyệt (bật AF-19 thì người duyệt khác người chuyển).
5. Bật cờ ở staging và diễn tập với dữ liệu giả: buyer nhập tài khoản → xác minh → nhận việc → xem tài khoản → ghi mã + chứng từ giả → người khác xác nhận → đơn hiện "Đã hoàn tiền". Diễn tập thêm một lần chuyển hết hạn giữ (`unknown`) và một mã giao dịch trùng. Giao dịch thật chỉ khi được cho phép riêng.
6. Bật ở production. Các refund đang `awaiting_provider_refund` đều đi qua luồng mới: buyer thấy yêu cầu nhập tài khoản trên trang đơn. Thông báo email/inbox chưa có, xem PW trong `99-pending-work.md`.

## Rollback

- Tắt cờ: không nhận tài khoản mới, không tạo lần chuyển mới. Các lần chuyển đang mở vẫn nhận việc, ghi mã và xác nhận được. Ghi `succeeded` một bước vẫn bị chặn cho mọi refund có lần chuyển mở (409 `attempt_active`), nên rollback không mở đường vòng.
- Giữ khoá khi tắt cờ: không có khoá thì không ai xem được tài khoản của lần chuyển đang mở (503).
- Không chạy down `000013` khi đã có dữ liệu; migration down sẽ tự từ chối.

## Theo dõi

- Log `manual_refund_report` mỗi phút: `unknown`, `submitted_over_24h`, `destinations_pending_over_24h`, `executing_past_lease`, `ready_over_24h`. Mức `warn` khi có `unknown`, có bản ghi đã `submitted` quá 24 giờ, hoặc có lần chuyển quá hạn giữ.
- Log khác:
  - `manual_refund_claim_expired`, `manual_refund_decided`, `refund_destination_revealed`;
  - `refund_destination_decrypt_failed` (khoá sai hoặc thiếu phiên bản);
  - `refund_evidence_upload_failed`.
- Audit Payment (target `payment_refund`):
  - `refund_destination_submitted`, `refund_destination_verified`, `refund_destination_rejected`, `refund_destination_revealed`;
  - `manual_refund_attempt_prepared`, `_executing`, `_submitted`, `_confirmed`, `_failed`, `_voided`, `_unknown`.
  - Audit không chứa số tài khoản.

## Kiểm thử

- Unit:
  - `go test ./services/payment/internal/domain/ ./services/payment/internal/adapter/ ./services/payment/internal/config/ ./services/payment/internal/transport/`
  - `go test ./gateway/internal/transport/`
  - `npx vitest run src/lib/manual-refunds.test.ts`
- Integration (`PAYMENT_TEST_DATABASE_URL`, tên DB kết thúc `_test`):
  - `TestManualRefundNeedsAVerifiedDestinationAndASecondAdmin`
  - `TestExpiredClaimIsUnknownUntilTheStatementIsChecked`
  - `TestManualRefundHistoryIsKept`

## Bồi hoàn ngoài capture (PW-032, 2026-10-10)

- `FEATURE_REIMBURSEMENTS_ENABLED=true`, trần mỗi khoản `PAYMENT_REIMBURSEMENT_MAX_AMOUNT` (mặc định 500000). Dùng cho tiền sàn trả buyer ngoài số đã thu (phí gửi trả hàng buyer tự trả, bồi hoàn thiện chí). Quỹ sàn, sổ riêng `reimbursements`: không tạo refund, không trừ hạn mức hoàn của capture, không trừ settlement của shop.
- Quy trình ở `/admin/refunds` → Reimbursements: admin `finance.prepare` lập (lý do, số tiền, mã đơn) → admin `finance.approve` **khác người lập** duyệt (xác thực lại khi bật AF-19) → chuyển khoản ngoài ứng dụng tới tài khoản hoàn tiền **đã xác minh** của buyer trên cùng đơn → ghi mã giao dịch ngân hàng (không trùng với giao dịch hoàn tiền nào).
- Buyer chưa có tài khoản đã xác minh trên đơn: khoản đã duyệt chờ (409 `destination_not_verified`); hàng việc `reimbursements_unpaid_24h` báo khoản đã duyệt quá 24 giờ chưa trả.
- Migration Payment `000018`; down từ chối khi đã có khoản bồi hoàn.
