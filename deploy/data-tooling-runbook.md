# Dữ liệu, migration và công cụ seed (plan 14) — triển khai và vận hành

Xem thêm [platform-runbook](platform-runbook.md) và [edge-runbook](edge-runbook.md).

## Thay đổi

| Phần | Trước | Sau |
|---|---|---|
| Quyền database | 11 service và migration đều đăng nhập bằng superuser `POSTGRES_USER` | Mỗi service một role `<service>_app`: chỉ CONNECT vào DB của nó, chỉ đọc/ghi dòng (không DDL, không TRUNCATE, không đụng ledger migration), không superuser. Migration chạy bằng `shopee_migrator` (chủ mọi DB, không superuser). Superuser chỉ dùng cho vận hành (backup, `db-roles.sh`) |
| Service khởi động | Không kiểm tra quyền | Hỏi PostgreSQL role của mình có quá quyền không (superuser, sở hữu bảng, vào được DB khác, sửa được ledger). Production: từ chối chạy; dev: log `database_role_too_powerful`. Production cũng từ chối mật khẩu DB `CHANGE_ME` hoặc ngắn |
| Kiểm tra dữ liệu | Preflight rời rạc cho từng lần migration | `deploy/data-preflight.sh`: 60+ kiểm tra chỉ-đọc cho 10 DB (trùng email/SKU/slug/provider event, lệch tổng tiền đơn, hoàn tiền vượt, payout trùng, tồn kho âm, giữ hàng quá hạn, dữ liệu seed...). Đọc ledger migration trước, không giả định schema |
| Loader seed (`fetching_data/`) | Mặc định ghi thật; `load-db` `DELETE` toàn bảng vendors/products/categories và xóa lịch sử giữ hàng/biến động kho | Mặc định chạy thử; ghi chỉ với `--apply`, sau guard (không production, đúng container local của checkout này), DB phải ở migration mới nhất, có backup và manifest. Nạp không bao giờ xóa; `reset-seed` xóa đúng khóa đã ghi nhận và từ chối khi còn đơn/review/lịch sử kho/audit tham chiếu |
| Parity Go/Python | Không kiểm | `backend/testdata/seed_parity.json`: slug, variant key, SKU, product input, option value, tiền; test Go (CI) và Python (máy local) cùng đọc |
| Chuẩn hóa | Option quá dài bị cắt rồi gộp lặng lẽ; kích thước bị sửa/xóa trong DB sau khi nạp | Không cắt, không xóa lặng lẽ: `results/normalize_report.json` liệt kê unresolved/merged; kích thước chuẩn hóa trong lớp normalize |

## Chuyển cụm đang chạy sang role riêng (một lần)

Volume mới tự tạo role (`postgres-init/002-db-roles.sh`). Cụm đang chạy:

1. Backup mọi DB.
2. `bash deploy/gen-db-passwords.sh` → dán 12 dòng vào `.env` (không commit). Chỉ chữ, số, `_`, `-` (nằm trong URL).
3. `bash deploy/db-roles.sh` → tạo `shopee_migrator` và 11 `<service>_app`, chuyển chủ của mọi DB và object sang migrator, cấp quyền dòng cho app, thu CONNECT của PUBLIC (kể cả `postgres`, `template1`). Idempotent: chạy lại an toàn.
4. Migration từ nay chạy bằng migrator: `make migrate-up-all` (Makefile đã đổi sang `MIGRATOR_DB_PASSWORD`).
5. `docker compose up -d` (DATABASE_URL của service đã là `<service>_app`). Log không được có `database_role_too_powerful`; production thì service không lên nếu role quá quyền.
6. `bash deploy/data-preflight.sh`.

Rollback: đặt lại `DATABASE_URL` cũ là không thể (compose đã đổi), nhưng role cũ vẫn còn: superuser vẫn đăng nhập và có mọi quyền; chạy image cũ với `DATABASE_URL` superuser trong một override nếu cần. Không xóa role mới.

Lưu ý:
- Ai đó chạy migration bằng superuser (sai quy trình) → bảng mới thuộc superuser, app không có quyền trên bảng đó (lỗi `permission denied`). Sửa: chạy lại `bash deploy/db-roles.sh` (trả mọi object về migrator và cấp lại quyền).
- Sau restore từ backup: chạy lại `bash deploy/db-roles.sh` (backup không mang role/password).
- Đổi mật khẩu một role: sửa `.env`, `bash deploy/db-roles.sh`, `docker compose up -d <service>`.

## Preflight dữ liệu

```
bash deploy/data-preflight.sh                  # mọi DB
bash deploy/data-preflight.sh order payment    # một số DB
bash deploy/data-preflight.sh --production     # dữ liệu seed cũng là lỗi (go-live, sau restore)
bash deploy/data-preflight.sh --report out.tsv
```

- Mỗi DB chạy trong transaction `READ ONLY` (timeout 120 s, lock 5 s). Chỉ báo cáo; sửa qua operation/migration có review, không SQL tay xuyên service.
- `blocking` > 0 → exit 1. `seed` > 0 → exit 1 khi `--production`. `warning`: tồn đọng vận hành. Exit 2: một kiểm tra không chạy được.
- DB chưa ở migration mới nhất của repo → `schema_not_at_latest_migration` (các kiểm tra khác của DB đó bỏ qua): migrate trước.
- Chạy: trước go-live, sau mỗi restore (OPS-03), sau migration có backfill, định kỳ.
- Kết nối khác Compose: `PSQL_CMD="psql -h <host> -U <user>"`; DB tên khác (bản restore): `ORDER_DB=order_db_restored`.

Kết quả trên bản sao dữ liệu local (2026-10-02, sau khi migrate bản sao lên mới nhất): 5.563 tài khoản `@scraped.local`, 955 liên hệ giả, 30.548 ảnh hotlink, 1.671 ảnh review hotlink, 5.329 review chưa xác minh (đều là seed); 1 shop đã duyệt chưa có địa chỉ lấy hàng; 5.331 đơn "đã thanh toán" không có bản ghi `order_payments` (5.329 đơn seed của review, 2 đơn thật tạo trước migration Order 000010 — migration đó không backfill). Production phải 0 ở mọi dòng seed và blocking.

## Công cụ seed (`fetching_data/`, chỉ local/dev/staging)

```
tiki-scraper load-db                 # chạy thử: số dòng, checksum, 0 delete
tiki-scraper load-db --apply         # guard → kiểm migration → backup → nạp → manifest
tiki-scraper load-db --resume <id>   # tiếp lần nạp lỗi, cùng file nguồn
tiki-scraper reset-seed              # chạy thử: số dòng sẽ xóa
tiki-scraper reset-seed --apply      # guard → kiểm tham chiếu → diễn tập (ROLLBACK) mọi DB → backup → xóa
```

- Guard (`tiki_scraper/target_guard.py`) từ chối khi: tiến trình có `ENV=production`; `.env` ghi `ENV=production`; container không phải PostgreSQL của project `shopee` từ checkout này hoặc đang dừng; bất kỳ service nào chạy `ENV=production`; không xác định được môi trường. Mọi script ghi DB (kể cả 4 script cũ ở `fetching_data/`) gọi guard ngay trước lần ghi đầu tiên và mặc định chạy thử.
- Manifest: `cache/import_runs/<run_id>/manifest.json` (nguồn, sha256 từng file, phiên bản schema, số dòng, trạng thái từng DB, lỗi), khóa đã ghi ở `keys/`, backup ở `backups/`.
- Reset từ chối khi còn: đơn/giỏ/review ở DB khác trỏ tới sản phẩm/shop/user seed; giữ hàng, biến động kho, kiểm kho, yêu cầu nhập; audit; tài khoản payout; hoặc dòng không phải seed của chính các bảng đó (ví dụ rule do lần nạp cũ không có manifest tạo). Dấu vết vận hành (outbox, cache, phiên đăng nhập) được xóa cùng seed. Reset toàn bộ môi trường local: xóa volume.
- Normalize ghi `results/normalize_report.json`. Trên cache hiện tại: 63 option dài > 500 ký tự (trước bị cắt), 568 giá trị kích thước không phải 3 chiều (trước bị xóa sau khi nạp), 474 cách viết được gộp theo quy tắc hoa/thường/khoảng trắng, 17 sản phẩm vượt giới hạn tên/mô tả của backend, 2 tổ hợp biến thể trùng.

## Theo dõi

| Tín hiệu | Ý nghĩa |
|---|---|
| Log `database_role_too_powerful` | Service đang chạy với role quá quyền (dev); production không lên |
| `permission denied for table ...` | Bảng do superuser tạo: chạy lại `deploy/db-roles.sh` |
| `data-preflight.sh` exit ≠ 0 | Xem các dòng `<-- FAIL` |
| `normalize_report.json` tăng đột biến | Nguồn dữ liệu đổi định dạng; xem trước khi nạp |
