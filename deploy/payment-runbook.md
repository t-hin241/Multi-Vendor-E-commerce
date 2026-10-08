# Payment upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/07-payment.md) và [runbook Order](order-runbook.md).

## Cấu hình

Không có secret mới. Payment dùng `IDENTITY_SERVICE_KEY` hiện có cho API nội bộ, cho lời gọi sang Order, Vendor và Identity.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `VENDOR_SERVICE_URL` | `http://vendor:8082` | Lấy tài khoản nhận tiền đã xác minh của vendor (đã che số) khi tạo đợt payout |
| `PAYMENT_WEBHOOK_RATE_PER_MINUTE` | `600` (10–100000) | Số webhook mỗi IP mỗi phút. Vượt ngưỡng trả 429 để provider gửi lại |

Khi `ENV=production` và `PAYMENT_PROVIDER=payos`, `PAYOS_RETURN_URL`, `PAYOS_CANCEL_URL`, `PAYOS_API_URL` bắt buộc là `https`. `PAYMENT_PROVIDER=mock` vẫn bị cấm ở production.

payOS không có môi trường sandbox tách riêng. Staging dùng một kênh payOS riêng với khóa riêng, không dùng chung khóa production. Webhook URL cấu hình trên my.payos.vn phải là `https://<domain>/api/webhooks/payments`.

## Thứ tự triển khai

Build image bằng `bash deploy/build-images.sh payment order vendor gateway frontend`.

1. Backup `payment_db`, `order_db`, `vendor_db`. Staging/production phải thử restore trước.
2. Kiểm tra `schema_migrations` từng DB, dừng nếu dirty.
3. Chạy [payment-preflight.sql](payment-preflight.sql) trên `payment_db`. Hai dòng đầu phải bằng 0.
   - `orders_with_several_pending_intents`: hỏi payOS từng link, hủy link thừa trên payOS, rồi chuyển intent thừa sang `failed` có lý do qua thay đổi được duyệt. Không xóa.
   - `refunded_intents_without_refund`: đối soát thủ công với payOS/ngân hàng. Migration không tạo biên lai giả.
4. Deploy Vendor (API nội bộ tài khoản nhận tiền mặc định). Bổ sung, không phá vỡ.
5. Migration Order lên `000012_settlement_effect`, deploy Order. Order gửi `vendor_order_id` khi yêu cầu hoàn tiền, và báo vendor order hoàn tất sang Payment. Nếu Payment chưa lên, tác vụ báo settlement được thử lại rồi park; replay sau bước 7.
6. Migration Payment lên `000008_settlement_ledger`, chạy [payment-validate.sql](payment-validate.sql), mọi dòng bằng 0.
7. Deploy Payment: `docker compose up -d --no-deps payment`.
8. Replay tác vụ settlement bị park (nếu có) ở trang admin Orders.
9. Deploy frontend. Smoke: tạo link thanh toán, mô phỏng thanh toán, đơn chuyển paid; trang admin Payment reconciliation và Vendor payouts mở được.

Vendor order hoàn tất trước khi nâng cấp không có trong ledger. Nếu cần đưa vào, chạy câu lệnh idempotent sau trên `order_db`, rồi để worker Order gửi:

```sql
INSERT INTO order_effects (order_id, kind, target)
SELECT order_id, 'settle_vendor_order', id FROM vendor_orders
WHERE status = 'completed' AND completed_at IS NOT NULL AND commission_rate_bps IS NOT NULL
ON CONFLICT (order_id, kind, target) DO NOTHING;
```

Vendor order không có snapshot hoa hồng sẽ bị park với lý do rõ. Xử lý bằng điều chỉnh sổ (Adjust) trong trang Vendor payouts.

## Kiểm thử thật với payOS

Chưa có kiểm chứng với payOS thật. Trước khi mở bán, trên kênh staging:

1. Tạo đơn nhỏ, tạo link, thanh toán thật số tiền nhỏ nhất.
2. Webhook phải được ghi `processed`, đơn chuyển `paid`. Nếu receipt bị từ chối vì chữ ký, dừng và kiểm tra khóa checksum.
3. Để một link hết hạn không trả tiền: worker phải đóng link (`expired`) và hủy trên payOS.
4. Kiểm tra màn hình reconciliation không còn mục bất thường.

## Theo dõi

Worker Payment chạy mỗi 30 giây: áp lại receipt chưa xong, hỏi payOS về link `creating` quá 2 phút và link `pending` quá hạn 5 phút. Mỗi phút ghi `payment_reconciliation_report` (warn khi có mục cần xử lý).

| Log | Ý nghĩa |
|---|---|
| `payment_webhook_invalid_signature` | Webhook sai chữ ký. Tăng đột biến: kiểm tra khóa checksum hoặc bị giả mạo |
| `payment_receipt_parked` | Webhook chưa khớp intent nào. Thử lại 24 giờ rồi chờ admin |
| `payment_receipt_applied` mức error với `outcome=amount_mismatch` | Sai số tiền/tiền tệ, không ghi nhận tiền. Đối soát với payOS |
| `payment_receipt_apply_failed` | Lỗi khi áp receipt; provider sẽ gửi lại, worker cũng thử lại |
| `payment_link_outcome_unknown`, `payment_link_unverified_closed` | Gọi payOS tạo link bị timeout; đối soát tự động trước khi tạo link mới |
| `payment_capture_found_by_reconciliation` | Tìm thấy tiền đã trả qua truy vấn payOS (webhook bị mất) |
| `payment_order_sync_failed` | Không gửi được kết quả sang Order; thử lại khoảng 4 giờ rồi chờ review |
| `payout_batch_created`, `payout_item_resolved` | Payout thủ công |

SQL read-only:

```sql
SELECT status, outcome, count(*) FROM payment_receipts GROUP BY status, outcome;
SELECT id, order_id, status, provider_reference, create_attempts, last_error FROM payment_intents
WHERE status IN ('creating', 'pending') AND updated_at < now() - interval '10 minutes';
SELECT payment_intent_id, attempts, requires_review, last_error FROM payment_order_sync WHERE delivered_at IS NULL;
SELECT vendor_id, currency, sum(amount) AS owed FROM settlement_entries GROUP BY vendor_id, currency ORDER BY owed DESC;
```

## Payout thủ công

1. Trang admin Vendor payouts: xem số dư, sao kê từng vendor.
2. Bấm Create payout batch. Payment lấy mọi khoản khấu trừ và mọi khoản bán đã qua hạn trả hàng, loại vendor order Order báo còn trả hàng/hoàn tiền mở. Order không phản hồi thì không tạo batch. Vendor chưa có tài khoản đã xác minh bị bỏ qua có lý do.
3. Chuyển khoản theo số tiền từng dòng. Số tài khoản đầy đủ xem ở trang tài khoản nhận tiền của vendor (admin, có audit).
4. Ghi Paid kèm mã tham chiếu ngân hàng, hoặc Failed kèm lý do. Failed trả các khoản về để đưa vào batch sau.

Hoàn tiền sau khi đã trả vendor trở thành khoản nợ và được trừ vào batch kế tiếp. Sổ chỉ cho thêm dòng; sửa sai bằng Adjust có lý do.

## Rollback

Không chạy migration down khi đã có receipt, settlement hoặc payout mới.

- Payment về bản cũ: bản cũ vẫn đọc `payment_events` (bản mới vẫn ghi song song) nên không áp lại webhook đã xử lý. Bản cũ không hiểu trạng thái `creating`/`expired`; đóng hoặc đối soát intent `creating` trước khi rollback. Intent tạo bởi bản mới vẫn tra được bằng `provider_intent_id`. Đợt payout đang mở phải xử lý xong trước.
- Order về bản cũ: không gửi báo settlement; tác vụ đã xếp hàng giữ nguyên. Payment vẫn chạy.
- Không đổi provider của intent đang mở khi rollback.

## Quyền admin theo bundle và phê duyệt hai người (AF-19)

Xem `deploy/admin-permissions-runbook.md`: migration, cờ `FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED`, lệnh `bootstrap-access`, luồng phê duyệt và rollback.

## Hoàn tiền chuyển khoản thủ công (AF-06)

Xem `deploy/manual-refund-runbook.md`: migration `000013`, cờ `FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED`, khoá `REFUND_DESTINATION_KEY`, các bước xác minh → chuyển → xác nhận, trạng thái `unknown` và rollback.
