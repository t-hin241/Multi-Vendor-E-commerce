# Identity upgrade: rollout và vận hành

Phạm vi: IAM-01 đến IAM-05 trong `docs/modular/_upgrades/01-identity.md`.
Runbook này đi kèm migration Identity `000003_identity_sessions`; không thay thế các gate
backup/restore, monitoring và smoke test của `docs/foundations/05-production-checklist.md`.

## Contract sau nâng cấp

- Access token sống 15 phút, chỉ giữ trong bộ nhớ frontend. Refresh token sống 30 ngày,
  xoay vòng mỗi lần refresh; cookie `shopee_refresh`, `HttpOnly`, `SameSite=Strict`,
  path `/api/auth`, `Secure` ở staging/production. Không có refresh token trong JSON.
- `POST /api/auth/refresh` và `/logout` dùng cookie, không nhận token từ JSON.
  Các request thay đổi trạng thái auth/admin cần `Origin` trong allowlist và
  `X-CSRF-Protection: 1`; frontend gửi `credentials: include`.
- Frontend và gateway phải cùng site HTTPS; ưu tiên cùng origin với reverse proxy `/api`.
  Hai domain khác site không tương thích cookie Strict. Frontend dùng Web Locks để
  tuần tự refresh/login/logout giữa các tab; cần kiểm thử trình duyệt đích hỗ trợ API này.
  Fallback khi không có Web Locks chỉ tuần tự trong một tab.
- Mọi service có API đăng nhập kiểm tra chữ ký JWT rồi introspect Identity mỗi request,
  **không cache**. Session bị revoke/khóa trả 401; Identity không khả dụng trả 503 trên
  endpoint bắt buộc đăng nhập. Endpoint optional-auth xử lý như khách ẩn danh.
  Việc thu hồi áp dụng với request xác thực tiếp theo, không hủy công việc đã bắt đầu.
- Reset token sống một giờ, dùng một lần. Request mới vô hiệu link reset cũ. Đổi password,
  consume token, revoke toàn bộ session và audit cùng transaction. Link dùng fragment
  `#token=...`; không đưa token vào query string, log, queue hoặc localStorage.

## Cấu hình bắt buộc

Cấp secret từ môi trường triển khai/secret manager. Các `CHANGE_ME` trong `.env.example`
chỉ là placeholder; không dùng để chạy service, không ghi secret thật vào repo hay terminal log.

| Biến | Nơi dùng | Yêu cầu |
|---|---|---|
| `ENV` | Backend services và Gateway qua Compose | `staging` hoặc `production`; mặc định local là `development` |
| `JWT_SIGNING_KEY_ID`, `JWT_SIGNING_KEY` | **Chỉ Identity** | Khóa riêng Ed25519 ký access token (`deploy/gen-jwt-keys.sh`); xem mục "Khóa ký token" |
| `JWT_PUBLIC_KEYS` | Mọi service xác thực | Khóa công khai `kid:base64`, chỉ xác minh được, không ký được |
| `IDENTITY_RATE_LIMIT_KEY` | Identity | HMAC email/IP thành key rate limit (Redis không giữ email thô); tối thiểu 32 ký tự ở staging/production |
| `IDENTITY_REDIS_PASSWORD` | Identity | User ACL `identity` trên Redis cache (`deploy/gen-redis-passwords.sh`) |
| `IDENTITY_SERVICE_URL` | Các service gọi Identity | URL private của Identity |
| `IDENTITY_SERVICE_KEY` | Identity và các service gọi Identity | Ngẫu nhiên, tối thiểu 32 ký tự |
| `IDENTITY_RESET_DELIVERY_KEY` | Chỉ Identity và Notification | Ngẫu nhiên, tối thiểu 32 ký tự, khác service key |
| `IDENTITY_RESET_ENCRYPTION_KEY` | Chỉ Identity | Base64 chuẩn của 32 byte ngẫu nhiên; AES-GCM |
| `IDENTITY_ALLOWED_ORIGINS` | Identity | Danh sách exact HTTPS origin, phân cách dấu phẩy |
| `ALLOWED_ORIGINS` | Gateway; Identity kế thừa nếu `IDENTITY_ALLOWED_ORIGINS` rỗng | Đồng nhất với origin frontend, không wildcard |
| `NEXT_PUBLIC_API_BASE_URL` | Build frontend qua Compose | Origin public của gateway, không thêm `/api`; đổi giá trị phải rebuild frontend |
| `IDENTITY_PASSWORD_RESET_URL` | Identity | URL HTTPS của `/reset-password`, không query/fragment |
| `NOTIFICATION_SERVICE_URL` | Identity | URL private của Notification |
| `IDENTITY_TRUSTED_PROXY_CIDRS` | Identity | Chỉ CIDR của gateway trên network triển khai |
| `GATEWAY_TRUSTED_PROXY_CIDRS` | Compose truyền thành `TRUSTED_PROXY_CIDRS` cho Gateway | Chỉ CIDR của reverse proxy TLS; rỗng nếu nhận trực tiếp |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM` | Notification | SMTP hỗ trợ STARTTLS; mặc định port 587 |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | Notification | Credential riêng cho môi trường, nếu SMTP yêu cầu |
| `SMTP_ALLOW_PLAINTEXT` | Notification | `false` production; chỉ bật cho SMTP sink local |

Không public cổng Identity/Notification và đường dẫn `/internal/*`. Service key chỉ bảo vệ
xác thực, không mã hóa traffic: dùng private network được bảo vệ hoặc TLS nội bộ. Bảo đảm
proxy không ghi Cookie, Authorization, request/response body hoặc reset link.
Redis phục vụ rate limit có TTL; PostgreSQL giữ source of truth cho session và reset job.
PostgreSQL/Redis hỏng không được cấu hình bypass xác thực/rate limit.

Rate limit hiện tại: 30 POST/phút/IP/endpoint; refresh 120; login/register/request-reset
thêm 5/phút/IP/account. Key là HMAC fingerprint, không chứa email thô. Không khóa account
toàn cục khi một IP nhập sai. Theo dõi tấn công phân tán ở gateway/WAF khi mở public.

## Migration và cutover

1. Trên staging, chụp backup Identity và thử restore; lưu image/tag và version migration.
   Production chỉ tiếp tục sau khi evidence backup/restore đã được xác nhận.
2. Kiểm tra collision email trước migration bằng truy vấn chỉ trả ID, trong Identity DB:

   ```sql
   SELECT array_agg(id ORDER BY id) AS user_ids, count(*) AS collision_count
   FROM users GROUP BY lower(btrim(email)) HAVING count(*) > 1;
   ```

   Nếu có collision, xử lý quyền sở hữu tài khoản qua quy trình hỗ trợ có audit. Không tự
   gộp/xóa account thương mại. Migration sẽ từ chối tạo unique index khi còn collision.
   Index dùng biểu thức normalized, nên không cần sửa giá trị email cũ để enforce unique;
   dữ liệu mới và lookup đều normalize.
3. Mở maintenance window, dừng traffic auth/protected API và drain request cũ. Thay đổi
   contract cookie/introspection cần **cutover đồng bộ** Identity, Notification, frontend,
   gateway và tất cả service xác thực; không rolling hỗn hợp với image cũ không introspect.
4. Dùng migration runner đã cấu hình đúng Identity DB để chạy `000003` trước image mới.
   Recipe local hiện có: `make -s -f Makefile.txt migrate-up SERVICE=identity` từ repo root;
   chỉ dùng khi env/network trỏ đúng môi trường, tắt shell tracing và giữ log riêng tư.
   Không chạy migration đồng thời từ nhiều container hoặc chạy toàn bộ service ngoài phạm vi.
5. Triển khai config/secret và image mới. Phiên cũ không có `sid`/`family_id` bị từ chối;
   người dùng phải đăng nhập lại. Frontend xóa `shopee.auth` cũ trong localStorage khi mount.
   Không cần reset password người dùng để migrate.
6. Chạy smoke test bên dưới, kiểm tra 401/403/429/503 và backlog mail trước khi mở traffic.

Rollback: giữ migration additive và dữ liệu revoked/audit. Ưu tiên fix-forward hoặc quay
về image đã hỗ trợ session contract này. Không chạy migration down `000003` trên production,
không restore backup cũ để khôi phục session và không mở traffic qua image bỏ introspection.
Nếu chưa có image tương thích để rollback, giữ maintenance cho đến khi sửa xong.

## Tạo admin đầu tiên

Server không tự seed admin. Sau migration, đưa các biến sau vào môi trường tiến trình
operator bằng secret manager: `IDENTITY_BOOTSTRAP_EMAIL`, `IDENTITY_BOOTSTRAP_PASSWORD`,
`IDENTITY_BOOTSTRAP_NAME`, `IDENTITY_BOOTSTRAP_OPERATOR` (mã người vận hành/ticket),
`IDENTITY_BOOTSTRAP_CONFIRM=true`. Không paste password vào command line hoặc tài liệu.

Chạy từ repo root với cấu hình Compose đúng môi trường (lệnh PowerShell):

```powershell
docker compose run --rm --no-deps --entrypoint /usr/local/bin/bootstrap-admin `
  -e IDENTITY_BOOTSTRAP_EMAIL -e IDENTITY_BOOTSTRAP_PASSWORD `
  -e IDENTITY_BOOTSTRAP_NAME -e IDENTITY_BOOTSTRAP_OPERATOR `
  -e IDENTITY_BOOTSTRAP_CONFIRM identity
```

Lệnh khóa bảng trong transaction, chỉ tạo khi chưa có bất kỳ admin nào và ghi
`identity_audit_logs.action = 'admin_bootstrap'`. Chạy lại sẽ từ chối, không nâng quyền
user có sẵn. Kiểm tra đăng nhập admin, audit và xóa các biến bootstrap khỏi môi trường
operator sau thao tác. Không thêm chúng vào môi trường server lâu dài.

## Thu hồi session và xử lý reset delivery

- Admin gọi `POST /api/auth/admin/users/{userID}/sessions/{sessionID}/revoke` bằng access token
  admin, Origin hợp lệ và CSRF header. `sessionID` là family ID, không phải refresh token.
  Lấy reference từ phiên cần thu hồi hoặc truy vấn có quyền vận hành trong Identity DB:
  `SELECT DISTINCT family_id FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`.
  Chỉ lấy reference, không xuất token hash/password hash. API ghi audit; gọi lại không hồi sinh phiên.
- Khóa user qua `PATCH /api/auth/admin/users/{userID}/active` với `{"is_active":false}` thu hồi
  mọi session. Mở lại tài khoản không khôi phục các phiên đã revoke.
- Identity poll job PostgreSQL mỗi 2 giây, lease 45 giây, timeout 30 giây, tối đa 5 lần thử;
  backoff 30 × số lần thử giây. Notification nhận `delivery_id`, fetch material qua API có
  delivery key, gửi SMTP rồi trả kết quả. Worker xóa ciphertext sau gửi thành công, dùng
  token, request mới, hết hạn hoặc hết lượt retry. Khi Identity ngừng chạy, cleanup sẽ tiếp
  tục lúc khởi động; TTL vẫn được kiểm tra trước khi cấp material/reset password.
- SMTP có thể gửi lặp nếu mail đã được nhận nhưng ACK bị mất hoặc worker chết trước khi
  lưu kết quả. Retry dùng cùng token một lần, không tạo thêm token/session. Không cam kết
  exactly-once email; không cho Notification lưu token vào database/job riêng.
- Khi SMTP lỗi, sửa provider/config, kiểm tra backlog; không đọc/decrypt payload thủ công.
  Job failed/expired không requeue bằng SQL: người dùng yêu cầu link mới. Khi rotate encryption
  key, drain job còn hạn trước; nếu có sự cố key, vô hiệu link cũ và yêu cầu reset mới qua flow
  vận hành có audit. Đổi key trực tiếp sẽ làm job đã mã hóa bằng key cũ không giải mã được.

## Quan sát

Tạo counter từ structured log trong hệ thống monitoring triển khai; bản nâng cấp chưa cài
monitoring stack hay `/metrics` mới. Không dùng email/user ID/token/session ID làm metric label.

| Counter/alert đề xuất | Nguồn |
|---|---|
| Login failure tăng đột biến | `message=http_request`, `path=/api/auth/login`, `status=401` |
| Refresh bị từ chối (gồm replay/expired) | `/api/auth/refresh`, `status=401`; không suy luận mọi 401 là replay |
| Rate limit / Identity outage | Auth `status=429/503`; protected API `status=503` |
| Reset retry hoặc worker lỗi | `reset_delivery_retry`, `reset_delivery_claim_failed`, `reset_delivery_persist_failed` |

Theo dõi status count và tuổi job pending/sending trong Identity DB; cảnh báo backlog > 5 phút,
job failed mới hoặc `encrypted_token IS NOT NULL AND expires_at <= now()` còn tồn tại khi worker
đang khỏe. Chỉ export số lượng/thời gian, không export ciphertext/email. Đặt baseline và ngưỡng
theo traffic staging trước khi paging production. Theo dõi latency introspection/DB vì xác thực
hiện phụ thuộc Identity mỗi request.

## Kiểm thử và evidence

Unit/contract test: `go test ./pkg/... ./gateway/... ./services/identity/... ./services/notification/...`
từ `backend`. Frontend: `npm run typecheck`, `npm run lint`, `npm test`, `npm run build`.

Integration riêng biệt, không kết nối DB ứng dụng (PowerShell từ root):

```powershell
docker compose -f deploy/identity-test.compose.yml up -d --wait
$env:IDENTITY_TEST_DATABASE_URL='postgres://identity_test@127.0.0.1:55431/identity_test?sslmode=disable'
$env:IDENTITY_TEST_REDIS_URL='redis://127.0.0.1:56391/0'
Push-Location backend
go test -count=1 ./services/identity/...
Pop-Location
docker compose -f deploy/identity-test.compose.yml down
```

Test stack dùng trust auth, bind localhost, PostgreSQL tmpfs và database `identity_test`; chỉ
dùng kiểm thử. Test tạo/xóa schema riêng, không đổi dữ liệu ứng dụng. CI Linux chạy cùng bộ test
với `-race`; Windows hiện thiếu CGO compiler nên không chạy race detector tại máy này.

Smoke staging bắt buộc trước production: register buyer/vendor; từ chối register admin;
login/refresh/reload/logout và nhiều tab; refresh đồng thời; forgot/reset qua SMTP thật hoặc sink;
link expired/đã dùng; mật khẩu cũ và session cũ bị từ chối sau reset; user bị khóa không gọi được
cart/checkout/vendor API bằng access token cũ; non-admin không gọi được admin API; Origin sai
bị 403; rate limit 429; Redis/Identity outage fail closed; kiểm tra DevTools không có token trong
localStorage/URL query và cookie có đầy đủ flags. Chạy trên domain HTTPS thực với proxy CIDR thật.

Evidence lần triển khai code: unit/contract backend, go vet, test frontend và build production
Next.js đã pass; bộ PostgreSQL/Redis
đã pass trong phiên làm việc trước khi Docker dừng. Lần xác minh cuối còn cần chạy lại integration
khi Docker hoạt động. Chưa bootstrap admin thật, kiểm chứng SMTP thật hoặc deploy production.
Frontend ESLint không có lỗi; còn warning `<img>` có sẵn trong `product-reviews.tsx`, ngoài phạm vi
Identity. Golangci-lint chưa có trên máy này; kết quả CI lint/race vẫn là gate trước merge/deploy.

### Migration database local — 2026-09-28

Theo yêu cầu vận hành, đã chạy migration `000003_identity_sessions` lên `identity_db` của
container `shopee-postgres-1`: version `2` → `3`, `dirty=false`. Trước migration không có
collision email normalized hoặc connection ứng dụng đang mở. Sau migration đã xác nhận
cột `family_id`, bảng reset delivery/audit và unique index email hợp lệ; số lượng user
(5.576), refresh token và reset token được giữ nguyên.

Backup trước migration: `.local-backups/identity/identity_db_before_000003_20260928T054957Z.dump`
(298.304 byte), đã kiểm tra archive bằng `pg_restore --list`; SHA-256 lưu cạnh file backup.
Thư mục backup được Git bỏ qua vì chứa dữ liệu riêng tư. Đây chưa phải evidence restore thử.
Ở bước migration này, chỉ PostgreSQL được khởi động; các service ứng dụng chưa được khởi động lại.
Kết quả sửa cấu hình, recreate service và smoke test local sau đó được tổng kết tại
[Identity module details](../docs/module-details/01-identity.md).

## Khóa ký token (Ed25519)

Access token ký bằng EdDSA. **Chỉ Identity** có khóa riêng (`JWT_SIGNING_KEY`), nên chỉ nó phát được token. Mọi service khác chỉ có `JWT_PUBLIC_KEYS`: xác minh được, không ký được.

Trước đây cả 11 service dùng chung `JWT_SECRET` (HS256). Bất kỳ service nào, hoặc bất kỳ ai lộ được env của một service, đều tự phát được token, kể cả token role admin.

Token mang `kid` (chọn khóa công khai để kiểm) và `iss=shopee-identity`. Verifier từ chối:
- `alg` khác EdDSA, kể cả `none` và HS256 dùng khóa công khai làm secret;
- `kid` lạ hoặc thiếu;
- sai issuer.

### Chuyển từ HS256 sang (một lần)

1. `bash deploy/gen-jwt-keys.sh` → dán `JWT_SIGNING_KEY_ID`, `JWT_SIGNING_KEY`, `JWT_PUBLIC_KEYS` vào `.env`. Thêm `IDENTITY_RATE_LIMIT_KEY=$(openssl rand -hex 32)`.
2. Giữ `JWT_SECRET` cũ và đặt `JWT_ACCEPT_LEGACY_HS256_UNTIL=<bây giờ + 30 phút, RFC 3339 UTC>` (tối đa 24 giờ; service từ chối khởi động nếu xa hơn).
3. `docker compose up -d`. Token cũ vẫn dùng được tới hết hạn (≤15 phút); token mới là EdDSA. Trong cửa sổ này, token cũ vẫn bị đối chiếu session/role với Identity, nên không thể dùng nó để nâng quyền.
4. Sau mốc trên: xóa `JWT_ACCEPT_LEGACY_HS256_UNTIL` và `JWT_SECRET` khỏi `.env`, rồi `docker compose up -d`. Thực ra hết mốc là HS256 đã tự bị từ chối.

Người dùng không bị đăng xuất: gặp 401, frontend tự refresh một lần, vì refresh token nằm trong DB và không phụ thuộc thuật toán ký.

### Xoay khóa (không ai bị đăng xuất)

1. `bash deploy/gen-jwt-keys.sh <id-mới>`. Nối khóa công khai mới vào `JWT_PUBLIC_KEYS` (`id-cũ:...,id-mới:...`), rồi `docker compose up -d`: mọi service chấp nhận cả hai.
2. Đổi `JWT_SIGNING_KEY_ID`/`JWT_SIGNING_KEY` sang cặp mới, rồi `docker compose up -d identity`.
3. Sau 15 phút, bỏ mục cũ khỏi `JWT_PUBLIC_KEYS` và `docker compose up -d`.

### Khi có sự cố

- **Lộ khóa riêng:** xoay ngay. Có thể bỏ khóa cũ khỏi `JWT_PUBLIC_KEYS` ngay ở bước 1 nếu chấp nhận người dùng phải refresh. Thu hồi session nghi vấn ở `/admin`. Mọi token vẫn bị kiểm session ở Identity, nên token giả cần một session thật khớp user/role.
- **Lộ khóa công khai:** không cần làm gì: khóa công khai không ký được token.
- **Rollback image cũ (HS256):** cần lại `JWT_SECRET` trong `.env` (image cũ đọc biến này). Vì vậy chỉ xóa nó khi bản mới đã ổn định.
