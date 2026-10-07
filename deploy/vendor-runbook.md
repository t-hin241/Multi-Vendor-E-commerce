# Vendor upgrade — local testing và rollout

Phạm vi: VEN-01…05; [chi tiết module](../docs/module-details/02-vendor.md).
Chưa chứng nhận go-live theo [production checklist](../docs/foundations/05-production-checklist.md).

## Cấu hình

Giữ nguyên các biến Identity/JWT/database/object storage đang có. Bổ sung/xác nhận:

| Biến | Nơi dùng | Yêu cầu |
|---|---|---|
| `VENDOR_PAYOUT_ENCRYPTION_KEY` | Chỉ Vendor | Base64 chuẩn của đúng 32 byte ngẫu nhiên; giữ bền qua restart/rebuild |
| `VENDOR_PAYOUT_SERVICE_KEY` | Vendor xác thực Payment payout scope | Secret riêng ≥32 ký tự; khác `IDENTITY_SERVICE_KEY` |
| `IDENTITY_SERVICE_KEY` | Các consumer Vendor/Identity, reporting, status event | Key hiện có, ≥32 ký tự |
| `IDENTITY_SERVICE_URL` | Vendor | Compose đã cấu hình URL Identity nội bộ |
| `CATALOG_SERVICE_URL` | Vendor | Compose `http://catalog:8083` |
| `ORDER_SERVICE_URL` | Vendor | Compose `http://order:8086` |
| `PAYMENT_SERVICE_URL` | Vendor | Compose `http://payment:8087` |

`.env.example` chỉ chứa `CHANGE_ME`. Operator cấp secret thực trong môi trường local/
secret manager; không commit, không in ra log. Task này không ghi secret thật vào `.env`.
Sai định dạng key làm Vendor fail startup với lỗi cấu hình rõ ràng.

Không thay encryption key tùy tiện khi đã có dữ liệu tài khoản: cần quy trình giải mã
bằng key cũ rồi tái mã hóa có kiểm soát. Backup DB cần đi kèm backup key an toàn.
Scope payout chưa cần inject vào Payment runtime hiện tại vì chưa có payout worker;
PAY-04 phải inject riêng khi triển khai worker, không phân phối key này cho mọi service.

## Preflight và migration

Đã kiểm tra local trước khi viết migration: Vendor version 6 clean, Catalog 10 clean,
Order 6 clean, Payment 3 clean. Ngày 2026-09-28 đã áp dụng lên DB ứng dụng local:
Vendor 7, Catalog 11, Order 7, Payment 4, tất cả clean. Backup và manifest kiểm chứng
ở `.local-backups/vendor-upgrade/20260928T160435Z/`; archive đã được kiểm tra đọc đầy đủ,
chưa thực hiện restore rehearsal. Dữ liệu cũ giữ nguyên. Chưa rebuild/start các service
ứng dụng mới; 958 outbox event chờ đồng bộ. Các bước dưới đây vẫn áp dụng khi rollout
sang môi trường khác, không cần chạy lại migration đã áp dụng trên local này.

| Service | Migration mới |
|---|---|
| Vendor | `000007_vendor_operations` |
| Catalog | `000011_vendor_sale_status` |
| Order | `000007_vendor_sale_status` |
| Payment | `000004_payout_destination_reference` |

1. Backup 4 DB và lưu image/tag đang chạy. Với staging/production phải thử restore.
2. Xem `schema_migrations` từng DB; dừng nếu dirty hoặc version khác dự kiến.
3. Kiểm tra shop thiếu description/default address; bổ sung qua API trước khi duyệt
   hoặc restore. Không tự gán địa chỉ/approval cho dữ liệu cũ.
4. Kiểm tra `vendor_payout_accounts` đã có dữ liệu hay chưa. Nếu có ciphertext tạo
   trước contract AES-GCM/AAD mới, phải review nguồn và quy trình tái mã hóa/version
   riêng; migration chỉ thêm version, không đoán cách giải mã dữ liệu cũ.
5. Kiểm tra payout_items legacy thiếu destination/currency. Giữ nguyên và chặn xử lý
   đến khi có đối chiếu từ operator. Constraint `NOT VALID` không sửa bản ghi cũ nhưng
   áp dụng cho ghi mới; không tự gán default mới vào batch cũ. Trigger cũng chặn đổi
   destination, nên remediation legacy cần migration có kiểm soát, không UPDATE tùy tiện.
6. Maintenance/drain traffic checkout và moderation trong cutover. Dùng migration
   runner hiện có của dự án, từng service riêng, không `migrate-up-all`:

   ```text
   make -s -f Makefile.txt migrate-up SERVICE=catalog
   make -s -f Makefile.txt migrate-up SERVICE=order
   make -s -f Makefile.txt migrate-up SERVICE=payment
   make -s -f Makefile.txt migrate-up SERVICE=vendor
   ```

   Chạy trong môi trường tin cậy với đúng `.env` và network; tắt shell tracing và tránh
   đưa credential vào log chia sẻ. Không chạy nhiều migration runner cùng service.

## Thứ tự triển khai

1. Identity bổ sung `is_active` cho internal user contract.
2. Catalog và Order mới có consumer/read model; chúng fail closed trong lúc chưa backfill.
3. Cập nhật Inventory, Shipment, Review và các client Vendor dùng service key; Payment
   có reporting contract mới. Giữ maintenance vì code cũ không có selling fence.
4. Vendor producer và worker. Migration backfill outbox cho mọi shop hiện có; consumer
   reconciliation sửa mất event. Chờ API admin operations không còn parked events,
   và các shop cần bán có `enforced_version >= version`.
5. Frontend mới; smoke test rồi mới mở traffic.

Compose rebuild dùng các service hiện có, ví dụ sau khi cấu hình và migration đúng:

```text
docker compose build identity vendor catalog order payment inventory shipment review frontend
```

Container như `shopee-vendor-1` là instance của service, không phải tên image.
Khi deploy, Compose có thể recreate container của cùng service để chạy image mới;
không cần tạo stack/vendor service khác hoặc xóa volume dữ liệu.

Không rollback sang image bỏ qua suspension/fencing. Ưu tiên fix-forward hoặc image
tương thích contract mới; giữ schema, trạng thái suspended và audit. Không chạy migration
down trên dữ liệu thương mại: down chỉ dành cho môi trường test trống, có thể mất audit/
version/read model. Nếu không có image tương thích, giữ maintenance.

## Quan sát và replay

`GET /api/vendor/admin/operations` qua admin session trả pending applications,
pending/parked status events, tuổi event chưa ACK lâu nhất, số thay đổi payout trong 24h.

Theo dõi log `vendor_status_propagation_pending`, `vendor_outbox_*_failed`,
`vendor_status_reconciliation_*_failed`. Event ID là request ID gửi cho consumer.
Worker claim tối đa 5 event, lease 60 giây; timeout HTTP 3 giây, tối đa 10 lần thử.
Sau khi sửa sự cố, admin `POST /api/vendor/admin/applications/:id/replay`; operation
idempotent ở consumer, không tạo quyết định/audit duyệt mới. UI có nút Retry sync.

Nếu chỉ một consumer ACK, shop vẫn `enforcement_pending`; không tuyên bố đã khóa xong.
Không sửa `enforced_version` bằng SQL để bỏ qua tình trạng pending.

## Kiểm thử local cô lập

Dùng PostgreSQL test sẵn của dự án, không thay database ứng dụng:

```powershell
docker compose -f deploy/identity-test.compose.yml up -d postgres
$env:VENDOR_TEST_DATABASE_URL = 'postgres://identity_test@localhost:55431/identity_test?sslmode=disable'
go -C backend test -count=1 ./services/vendor/... ./pkg/vendorsales ./pkg/identityclient
```

Test chỉ chấp nhận database có tên kết thúc `_test`; tạo schema ngẫu nhiên, migrate và
xóa đúng schema đó. Test Vendor cũng nạp migration Order/Payment trong schema riêng để
kiểm tra concurrency fence và immutable batch. CI có PostgreSQL riêng và race detector.

Smoke sau cutover:

- Hai shop cùng user: chỉ shop được duyệt bán được; account/address không dùng chéo shop.
- Non-admin không duyệt, non-owner không sửa/xem payout; internal thiếu key bị chặn.
- Pending/rejected không publish/checkout; rejected bổ sung hồ sơ và nộp lại được.
- Suspend khi checkout: chờ enforcement ACK; sau ACK không tạo đơn bằng version cũ.
- Shop suspended vẫn xem/xử lý đơn cũ; restore cần admin và lý do.
- Thay tài khoản: pending mới không thay default verified; xác minh bản mới không đổi
  đích payout cũ; lookup exact version cũ vẫn đúng hoặc chặn review nếu chưa verified.
- Dashboard thay shop/ngày đúng query; Payment outage và settlement unknown hiện rõ.
- Mở storefront, buyer checkout/payment/fulfillment theo checklist chung.

Trước production vẫn cần rehearsal toàn stack, backup/restore, giám sát/alert thật,
TLS/private network và PAY-04 đối soát/chuyển tiền. Đợt Vendor này không tự tạo policy
giữ tiền hoặc phát sinh giao dịch tiền thật.

## Chính sách sàn và shop theo phiên bản (AF-02)

Đặc tả: `docs/modular/add_features/02-marketplace-policies.md`. Migration `000010_policy_versions`; Order cần `000017_policy_snapshots` và consumer `order-policy-versions` (đã cấp trong `deploy/nats/nats.conf`).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `FEATURE_VERSIONED_POLICIES_ENABLED` | `false` | Vendor: cho publish và hiển thị chính sách theo phiên bản; trang shop chỉ hiện chính sách shop đã duyệt. Đặt cùng giá trị ở Order |

Luồng: admin tạo draft ở `/admin/policies` (nội dung cố định khi tạo) → Publish kèm lý do → Vendor hỏi Order `GET /internal/policy-rules/readiness` cho từng rule được dẫn (`order.returns_window=window-<N>d`, `order.return_shipping_refund=none`). Thiếu ACK thì version ở `preparing`, bản cũ vẫn hiệu lực, worker hỏi lại mỗi 30 giây. Đủ ACK thì `published`, ghi `policy_outbox` (event `vendor.policy_published`), thông báo chủ shop và audit (`policy_audit_logs`, xem trong tra cứu audit admin).

- Version đã publish không sửa/xóa được (trigger). Sửa nội dung = draft mới. Không publish draft có `effective_at` trong quá khứ.
- Rule của Payment/Shipment chưa có contract readiness nên chưa thể dẫn trong chính sách; nội dung không được hứa hoàn phí vận chuyển cho tới khi Order hỗ trợ.
- Chính sách shop: chủ shop đề xuất, admin duyệt/từ chối (có lý do). Nội dung chứa cụm từ bớt quyền người mua ("không đổi trả", "không hoàn tiền", ...) bị từ chối ngay (422 `policy_reduces_protection`). Migration chuyển `policy_text` cũ thành đề xuất `legacy` chờ duyệt, không tự duyệt.
- Theo dõi: log `vendor_policy_preparing_overdue` (preparing quá 1 giờ), `vendor_policy_propagation_pending`, backlog `policy_outbox`.

Bật: deploy Order (consumer + readiness) trước, rồi Vendor; publish chính sách đổi trả đầu tiên khớp `ORDER_RETURN_WINDOW_DAYS`; xác nhận Order đã nhận (bảng `policy_versions`); sau đó mới bật flag ở Order. Rollback: tắt flag; không chạy down `000010` khi đã có version publish (down tự từ chối).
