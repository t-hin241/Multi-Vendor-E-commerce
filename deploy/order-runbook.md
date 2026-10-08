# Order upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/06-order.md) và [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình

Không có secret mới. Order, Payment, Shipment, Inventory, Catalog và Review dùng `IDENTITY_SERVICE_KEY` hiện có làm service key cho mọi API nội bộ.

| Biến | Service | Mặc định | Ý nghĩa |
|---|---|---|---|
| `PAYMENT_SERVICE_URL` | Order | bắt buộc | Gửi yêu cầu hoàn tiền sang Payment. Thiếu biến này Order không khởi động |
| `ORDER_RETURN_WINDOW_DAYS` | Order | `7` (1–365) | Số ngày sau khi gói hàng hoàn tất buyer còn được yêu cầu trả hàng. Mỗi yêu cầu lưu `policy_version` dạng `window-7d` |

Compose đã khai báo cả hai biến cho Order. Order không `depends_on` Payment vì Payment đã phụ thuộc Order; yêu cầu hoàn tiền là tác vụ bền vững nên Payment lên sau vẫn được gửi lại.

## Thay đổi contract giữa các service

| Contract | Bên cung cấp | Bên gọi | Tương thích |
|---|---|---|---|
| Mọi route `/internal/*` của Order bắt buộc service key | Order | Payment, Inventory, Shipment, Catalog, Review | Bên gọi mới gửi key cho Order cũ vô hại. Catalog và Review cũ không gửi key sẽ bị 403 với Order mới |
| `POST /internal/shipments/quotes`; `/internal/shipments/*` bắt buộc service key | Shipment | Order | Order cũ không gửi key nên không tạo được shipment với Shipment mới. Xem bước 9 |
| `GET /internal/carts/:buyerId/lines` | Cart | Order | Bổ sung, không phá vỡ |
| `POST /internal/inventory/returns` | Inventory | Order | Bổ sung |
| `POST /internal/payments/refunds`, admin `GET/POST /api/payments/admin/refunds…` | Payment | Order, admin | Bổ sung |
| `mark-paid` nhận `{payment_id, amount, currency}` | Order | Payment | Order mới vẫn nhận body rỗng của Payment cũ và log `order_mark_paid_legacy_contract`. Order cũ bỏ qua body |
| `POST /internal/refund-events` | Order | Payment | Mới |
| CORS cho header `Idempotency-Key`, expose `X-Total-Count` | Gateway | Frontend | Phải lên trước frontend mới |

## Thứ tự triển khai

Build image trước bằng `bash deploy/build-images.sh`, hoặc `bash deploy/build-images.sh order payment` cho vài service. Script build từng image một và thử lại khi lỗi mạng. Không dùng `docker compose build` hay `up --build` cho toàn bộ stack: build song song dễ hết thời gian chờ với lỗi `context deadline exceeded`. Các lệnh `up` bên dưới dùng image đã build sẵn.

1. Backup `order_db`, `payment_db`, `inventory_db`, `shipment_db`, `cart_db`. Staging/production phải thử restore trước cutover.
2. Kiểm tra `schema_migrations` từng DB, dừng nếu dirty hoặc sai version.
3. Chạy [order-preflight.sql](order-preflight.sql): phần đầu trên `order_db`, phần `payment_db` trên `payment_db`. Các nhóm "Blocking" phải bằng 0. Ghi lại số liệu nhóm thông tin:
   - `refunded_orders_without_receipt`: đơn bị đánh dấu `refunded` bằng thao tác admin cũ, không có giao dịch hoàn tiền. Migration không bịa số tiền đã hoàn; đối soát với cổng thanh toán hoặc ngân hàng bằng tay.
   - `legacy_returns_awaiting_refund`: sẽ chuyển thành `approved`, tức chưa nhận hàng và chưa hoàn tiền.
   - `completed_packages`: `completed_at` được lấy từ `updated_at`, là mốc bắt đầu cửa sổ trả hàng.
   - `orders_captured_twice`: hoàn tiền cho các đơn này phải chỉ rõ payment.
4. Deploy Gateway, Catalog, Review, Cart, Inventory. Các bản này tương thích với Order cũ.
5. Migration Payment lên `000006_order_refunds` (`make migrate-up SERVICE=payment`), deploy Payment.
6. Tạm ngừng checkout nếu có thể (bảo trì ngắn), vì bước 7 và 8 cần làm liền nhau.
7. Deploy Shipment.
8. Migration Order lên `000011_refunds_returns`, chạy [order-validate.sql](order-validate.sql) (mọi dòng bằng 0), rồi deploy Order: `docker compose up -d --no-deps order`.
9. Bù shipment cho đơn được thanh toán giữa bước 7 và 8. Câu lệnh idempotent: Shipment trả lại shipment đã có của vendor order.

   ```sql
   -- order_db
   INSERT INTO order_effects (order_id, kind, target)
   SELECT order_id, 'create_shipment', id FROM vendor_orders
   WHERE status IN ('paid', 'processing')
   ON CONFLICT (order_id, kind, target) DO NOTHING;
   ```

10. Deploy frontend mới. Smoke: checkout hiển thị phí ship từng shop và tổng, đặt hàng, thanh toán mock, admin xem chi tiết đơn, vendor thấy yêu cầu trả hàng.

Nếu Order mới lên trước Shipment mới, checkout báo không giao được vì chưa có API báo giá. Không có đơn nào được tạo trong trường hợp này.

## Theo dõi

Worker Order chạy mỗi 5 giây, mỗi vòng xử lý tối đa 20 tác vụ cho từng loại: consume giỏ, side effect, khôi phục checkout. Mỗi phút ghi log backlog.

| Log | Ý nghĩa |
|---|---|
| `order_effect_backlog` (warn khi có parked hoặc pending quá 5 phút) | Side effect tồn đọng: tạo/hủy shipment, trả tồn kho, thông báo, hoàn tiền, nhập lại kho hàng trả |
| `order_effect_parked` | Tác vụ bỏ cuộc sau 25 lần hoặc bị từ chối vĩnh viễn. Xử lý nguyên nhân rồi replay ở trang admin Orders |
| `order_payment_rejected`, `order_payment_exceptions_pending` | Payment báo tiền đã thu nhưng không thanh toán được đơn: đơn đã hủy, sai số tiền, thanh toán lần hai hoặc hết giữ hàng. Hoàn tiền ở trang chi tiết đơn |
| `order_checkout_recovered`, `order_checkout_recovery_failed` | Checkout bị gián đoạn quá 2 phút được xử lý lại theo trạng thái reservation |
| `order_refund_outcome_mismatch` | Payment báo kết quả hoàn tiền không khớp số tiền hoặc tiền tệ. Không tự áp dụng; cần đối soát |
| `order_mark_paid_legacy_contract` | Payment cũ còn gửi mark-paid không có capture. Phải hết sau khi Payment mới lên |
| `payment_refund_requested`, `payment_refund_resolved` (Payment) | Hoàn tiền được nhận và được xác nhận |
| `payment_refund_sync_failed` (Payment) | Không gửi được kết quả hoàn tiền về Order |
| `inventory_return_restocked` (Inventory) | Hàng trả được nhập lại kho |

SQL read-only:

```sql
-- order_db
SELECT kind, status, count(*), min(next_attempt_at) FROM order_effects WHERE status <> 'done' GROUP BY kind, status;
SELECT id, order_id, kind, attempts, last_error FROM order_effects WHERE status = 'parked' ORDER BY updated_at LIMIT 50;
SELECT payment_id, order_id, amount, rejection_reason, received_at FROM order_payments WHERE outcome = 'rejected' ORDER BY received_at;
SELECT status, count(*), sum(amount) FROM order_refunds GROUP BY status;
SELECT status, count(*) FROM checkout_operations GROUP BY status;

-- payment_db
SELECT status, count(*), sum(amount) FROM payment_refunds GROUP BY status;
SELECT payment_refund_id, attempts, last_error FROM payment_refund_sync
WHERE delivered_at IS NULL AND (requires_review OR attempts >= 10);
```

## Quy trình hoàn tiền thủ công

Chưa có API hoàn tiền tự động với cổng thanh toán. Money chỉ được coi là đã hoàn khi có chứng từ.

1. Order tạo yêu cầu: return đã nhận hàng, tranh chấp do admin tạo, hoặc capture bị từ chối. Order gửi sang Payment. Payment kiểm tra tổng hoàn không vượt số tiền đã thu và cùng tiền tệ.
2. Operator hoàn tiền qua dashboard cổng thanh toán hoặc chuyển khoản.
3. Tại trang admin Refunds, chọn Succeeded và nhập mã tham chiếu của cổng thanh toán hoặc ngân hàng. Nếu thất bại, chọn Failed và ghi lý do. Quyền admin được xác minh lại qua Identity.
4. Payment gửi kết quả về Order. Order cộng `refunded_amount`, chuyển gói hàng sang `refunded` khi hoàn đủ, cập nhật return và gửi thông báo.

Hoàn tiền thất bại thì return chuyển `refund_failed`; admin bấm Retry refund ở trang Returns sau khi xử lý nguyên nhân.

## Rollback

Không chạy migration down khi đã có dữ liệu của luồng mới. Down xóa lịch sử hoàn tiền, audit trả hàng và chứng từ Payment. Down `000011` còn thất bại nếu một item có nhiều yêu cầu trả hàng.

- Order về bản cũ vẫn ghi được đơn và return nhờ default, sequence và trigger của migration. Hạn chế:
  - Admin cũ duyệt return sẽ lỗi, vì trạng thái `approved_awaiting_provider_refund` không còn hợp lệ.
  - Order cũ không gửi service key nên không tạo được shipment nếu Shipment giữ bản mới. Rollback Shipment cùng lúc, hoặc sau khi lên lại Order mới thì chạy câu SQL ở bước 9.
  - Side effect, checkout operation và yêu cầu hoàn tiền đang dở dừng lại, rồi tiếp tục khi Order mới lên lại.
- Payment về bản cũ: yêu cầu hoàn tiền mới từ Order bị 404 và tác vụ `request_refund` bị park. Replay sau khi Payment mới lên lại.
- Frontend về bản cũ: checkout vẫn chạy nhưng không có idempotency key hay xác nhận tổng. Admin cũ không còn nút chuyển "refunded" hợp lệ.

## Hỗ trợ và khiếu nại theo đơn (AF-01)

Đặc tả: [01-order-support-cases](../docs/modular/add_features/01-order-support-cases.md). Order sở hữu case, message, timeline và ảnh bằng chứng; migration `000016_support_cases`. Không có secret mới ngoài khóa MinIO/S3 đã có.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `FEATURE_ORDER_SUPPORT_ENABLED` | `false` | Nhận case mới. Tắt chỉ chặn case mới; case cũ vẫn đọc, trả lời, giải quyết được |
| `FEATURE_ORDER_SUPPORT_PILOT_VENDOR_IDS` | rỗng | Danh sách vendor id (phẩy) được nhận case mới khi pilot; rỗng = mọi shop |
| `SUPPORT_ATTACHMENT_RETENTION_DAYS` | `180` | Số ngày giữ ảnh sau khi case đóng |
| `SUPPORT_ATTACHMENT_STORAGE_ENDPOINT`, `_ACCESS_KEY`, `_SECRET_KEY`, `_USE_SSL`, `SUPPORT_ATTACHMENT_BUCKET` | compose: `minio:9000`, bucket `support-evidence` | Bucket riêng tư cho ảnh. Order từ chối khởi động nếu bucket có bucket policy (tránh trỏ nhầm bucket media public). Bỏ trống endpoint = chạy không có ảnh |

Vận hành:

- Trước khi bật: có người trực hàng chờ `/admin/support` và email liên hệ thật; bật cho nhóm shop pilot bằng `FEATURE_ORDER_SUPPORT_PILOT_VENDOR_IDS`.
- Dashboard admin có ô `support_cases_unassigned`, `support_cases_overdue`, `support_cases_resolution_pending` (nguồn `/api/orders/admin/operations`).
- Case thuộc nhóm ảnh hưởng tiền (mọi category trừ `other`) giữ payout của vendor order cho đến khi case `closed` (lý do `support_case_open` trong `/internal/settlements/holds`). Case `resolved` tự đóng sau 7 ngày bởi worker của Order, nên hold không kéo dài vô hạn.
- Kết luận cần tiền/hàng: admin tạo refund tranh chấp ở trang đơn (hoặc buyer tạo return), rồi liên kết vào case. Case chỉ `resolved` khi Payment xác nhận refund / return đã `refunded`; refund thất bại đưa case về `in_progress`.
- Log cần theo dõi: `order_support_resolution_failed`, `order_support_attachment_put_failed`, `order_support_attachment_delete_failed` (object mồ côi trong bucket riêng tư, xóa tay theo `attachment_id`), `order_support_auto_close_failed`.
- Upload chưa gắn vào tin nhắn bị xóa sau 24 giờ; ảnh của case đã đóng bị xóa sau thời hạn giữ. Hàng `case_attachments` giữ lại làm tombstone.

Rollback: đặt `FEATURE_ORDER_SUPPORT_ENABLED=false`. Không chạy down `000016` khi đã có case: down tự từ chối để không mất hồ sơ khiếu nại và hold payout. Order bản cũ bỏ qua bảng mới; khi đó payout của vendor order có case mở **không** còn bị giữ, nên chỉ rollback image khi không còn case ảnh hưởng tiền đang mở.

### Sổ hold payout do Payment sở hữu (PW-001)

Migration Order `000019_settlement_hold_ledger`, Payment `000014_settlement_holds`. Cờ `FEATURE_SETTLEMENT_HOLD_LEDGER_ENABLED` ở Order, mặc định `false`.

- **Cách chạy:**
  - Case ảnh hưởng tiền nhận `hold_id` và trạng thái `preparing` trong cùng transaction tạo case.
  - Effect `acquire_settlement_hold` gọi `POST /internal/payments/settlement-holds` (có retry). Payment lấy khóa payout của vendor rồi ghi hold.
  - Tạo payout batch cũng đọc hold dưới cùng khóa đó, nên hold và claim không chen nhau.
  - Khi case `closed`, effect `release_settlement_hold` nhả hold. Lặp lại vẫn trả cùng biên nhận; nhả trước khi giữ để lại tombstone.
- **Trạng thái** (admin thấy ở trang case):
  - `preparing`: Payment chưa xác nhận. Mở refund từ case trả 503 `hold_unavailable`; resolution refund/return cũng bị chặn.
  - `active`.
  - `needs_review`: Payment trả `payout_already_claimed`, tức payout đã claim vendor order trước khi case mở, hoặc Payment từ chối hold. Refund cho buyer vẫn mở được.
  - `releasing`, `released`.
- **Xử lý `needs_review`:** người vận hành Payment kiểm item payout chứa vendor order đó.
  - Item còn `pending`: ghi `failed` có audit để nhả entry.
  - Tiền đã đi: ghi khoản phải thu của vendor bằng điều chỉnh sổ.
  - Danh sách hold xem tại `GET /api/payments/admin/settlement-holds` (`finance.read`).
- **Worker** (mỗi phút):
  - Cấp hold cho case mở trước khi bật cờ (backfill).
  - Gửi nhả hold cho case đã đóng mà hold chưa được nhả.
  - Log `order_support_hold_backlog`: `preparing`, `needs_review`, `releasing`; mức `warn` khi có `needs_review`.
  - Effect hold bị parked hiện trong `order_effect_backlog`.
- **Bật cờ:**
  1. Deploy Payment có `000014`.
  2. Deploy Order có `000019`.
  3. Bật cờ ở staging, mở một case `damaged`, kiểm hold `active` ở Payment và payout batch bỏ qua vendor order đó.
  4. Đóng case, kiểm hold `released`.
- **Tắt cờ:** case mới không cấp hold nữa; hold đã có vẫn được nhả khi case đóng. Truy vấn cũ `/internal/settlements/holds` vẫn giữ payout cho case mở, nên tắt cờ không mở payout. Không chạy down `000019`/`000014` khi đã có hold (down tự từ chối).

### Mở refund từ trang case (PW-014)

`POST /api/orders/admin/support-cases/:caseID/refunds {amount, reason, expected_version}` với header `Idempotency-Key` bắt buộc, quyền `finance.prepare`.

- Trong một transaction: tạo refund tranh chấp cho vendor order của case, liên kết refund làm resolution và chuyển case sang `resolution_pending`.
- Gửi lại cùng key trả lại đúng refund và case đó.
- Cần hold đã `active` hoặc `needs_review` khi bật cờ hold.
- Case chỉ `resolved` khi Payment xác nhận tiền đã hoàn.

### Yêu cầu không có mã đơn (PW-012)

Migration `000020_support_intakes`.

- **Buyer:** `POST /api/orders/support-intakes {reference_kind, reference, message}` (trang `/support`, mục "Không tìm thấy đơn hàng?"). Tối đa 3 yêu cầu đang mở; mỗi mã chỉ một yêu cầu mở.
- **Admin** (`support.manage`), ở đầu `/admin/support`:
  - Tra mã qua tìm kiếm thanh toán; tìm kiếm cần `finance.read`, nếu không có thì nhập order id.
  - **Gắn** (`POST …/support-intakes/:id/links`): Order từ chối nếu đơn không phải của chính buyer gửi yêu cầu (422 `intake_order_mismatch`). Khi gắn, Order mở case mới với lời của buyer, hoặc thêm vào case đang mở cùng vendor order và cùng loại.
  - **Đóng** (`…/closure`): lý do đóng được hiện cho buyer.
- Cả gắn và đóng đều có audit `support_intake`.
- Gateway: dùng chung giới hạn `support` (60 lần/phút).

## Snapshot chính sách khi đặt đơn (AF-02)

Migration `000017_policy_snapshots`: read model `policy_versions` (từ `vendor.policy_published`, consumer `order-policy-versions`, HTTP dự phòng `POST /internal/policy-published`) và cột `policy_snapshot` trên `orders`/`vendor_orders`.

- `FEATURE_VERSIONED_POLICIES_ENABLED=true`: mỗi đơn mới lưu phiên bản chính sách đang hiệu lực (theo `effective_at` so với thời điểm đặt, UTC) và rule đổi trả. Preview trả `policy_versions`; checkout nhận `accepted_policy_versions`, khác phiên bản đang hiệu lực thì 409 `policy_changed`. Chưa có chính sách đổi trả publish thì snapshot ghi rõ rule từ `ORDER_RETURN_WINDOW_DAYS` (`source=config`).
- Yêu cầu trả hàng và ngày đủ điều kiện payout (`eligible_at`) dùng cửa sổ trong snapshot của vendor order; đơn không có snapshot (đặt trước khi bật) giữ rule cũ từ config. Không backfill.
- `GET /api/orders/:id/policy-snapshot` (buyer chủ đơn, vendor của gói hàng qua `/api/orders/vendor/:id/policy-snapshot`, admin).
- Theo dõi tỉ lệ 409 `policy_changed` ở checkout (tăng ngay sau khi một phiên bản có hiệu lực là bình thường).

Rollback: tắt flag (đơn mới quay về rule config; đơn đã có snapshot giữ nguyên). Không chạy down `000017` khi đã có snapshot.

## Hủy gói hàng đã thanh toán (AF-03)

Xem `deploy/paid-cancellation-runbook.md`: migration `000021`, cờ `FEATURE_PAID_CANCELLATION_ENABLED`, fence bàn giao (`/internal/orders/fulfillment-grants/:id/claims`), trạng thái yêu cầu, thứ tự deploy (Order trước Shipment) và rollback.
