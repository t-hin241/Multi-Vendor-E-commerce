# Lớp biên (Caddy) — chỉ 80/443 mở ra ngoài

Xem thêm [platform-runbook](platform-runbook.md) và [chi tiết module](../docs/module-details/13-platform.md).

## Vì sao

- Mọi cổng trong `docker-compose.yml` chỉ nghe `127.0.0.1`: PostgreSQL, Redis, NATS, MinIO, 11 service, gateway, frontend.
- Production thêm `deploy/compose.edge.yml`: Caddy là thứ duy nhất mở ra ngoài (80/443) và tự lấy, tự gia hạn chứng chỉ TLS.
- **Docker tự ghi iptables:** cổng đã publish vẫn vào được dù ufw chặn. Đừng dựa vào firewall để đóng cổng Docker; dùng `bash deploy/check-exposure.sh` để biết chắc.

| Đường dẫn | Tới | Ghi chú |
|---|---|---|
| `/api/*`, `/healthz` | gateway | Rate limit, CORS, chặn `/internal` |
| `/media/product-images/*`, `/media/vendor-branding/*`, `/media/review-images/*` | MinIO | Chỉ GET/HEAD. Ghi, xóa, liệt kê, bucket khác, console → 404 |
| đường dẫn có đoạn `internal` | — | 404 |
| còn lại | frontend | |

- **Mạng:** Caddy chỉ ở mạng `edge` (cùng gateway, frontend, MinIO), không chạm database, Redis, NATS hay service khác.
- **IP thật của người dùng:** Caddy thay mọi `X-Forwarded-For` client gửi bằng địa chỉ nó thấy. Gateway chỉ tin header này khi request đến từ IP cố định của Caddy (`EDGE_PROXY_IP`), nên rate limit tính theo IP thật của người dùng.

## Cấu hình `.env` trên VPS

| Biến | Giá trị |
|---|---|
| `COMPOSE_FILE` | `docker-compose.yml:deploy/compose.edge.yml`. Mọi lệnh `docker compose` tự gồm lớp biên. Nếu thiếu, một lệnh `up -d` đơn lẻ sẽ đưa gateway ra khỏi mạng `edge` và trang web ngừng |
| `SITE_DOMAIN` | Tên miền, ví dụ `shop.example.com` (DNS A/AAAA trỏ về VPS trước khi khởi động) |
| `ACME_EMAIL` | Email nhận thông báo chứng chỉ |
| `ALLOWED_ORIGINS` | `https://<SITE_DOMAIN>` |
| `NEXT_PUBLIC_API_BASE_URL` | `https://<SITE_DOMAIN>` (không có `/api`). Được đóng vào image frontend lúc build, nên phải build lại frontend khi đổi |
| `OBJECT_STORAGE_PUBLIC_BASE_URL` | `https://<SITE_DOMAIN>/media` |
| `IDENTITY_PASSWORD_RESET_URL` | `https://<SITE_DOMAIN>/reset-password` |
| `INTERNAL_BIND_ADDRESS`, `PUBLIC_BIND_ADDRESS` | `127.0.0.1` (mặc định; không bao giờ đặt `0.0.0.0` trên server) |
| `EDGE_SUBNET`, `EDGE_PROXY_IP` | Mặc định `192.168.250.0/28` và `192.168.250.10`. Chỉ đổi khi dải này trùng mạng có sẵn trên host, và đổi cả hai cùng nhau |

- Production từ chối khởi động gateway nếu `TRUSTED_PROXY_CIDRS` trống hoặc tin mọi địa chỉ. `compose.edge.yml` tự đặt biến này bằng `EDGE_PROXY_IP`.
- **Ảnh đã tải lên trước đây** lưu URL theo `OBJECT_STORAGE_PUBLIC_BASE_URL` cũ (ví dụ `http://localhost:9000/...`): chuyển bằng `deploy/rewrite-media-urls.sh` (mục dưới).

## Đổi URL ảnh đã lưu

- **Phạm vi:** `deploy/rewrite-media-urls.sh` sửa 5 cột URL trong 3 database:
  - `catalog_db`: `product_images.url`, `product_media.url`.
  - `vendor_db`: `vendors.logo_url`, `vendors.banner_url`.
  - `review_db`: `review_images.url`.
  - Order, Cart và các service khác không lưu URL ảnh.
- **Dòng nào được đổi:** chỉ dòng có URL đúng bằng `<base cũ>/<bucket>/<object_key của chính dòng đó>`. Mọi URL khác được giữ nguyên và liệt kê theo tiền tố, ví dụ:
  - ảnh ngoài (dữ liệu seed `salt.tikicdn.com`) → `unrecognised`;
  - host cũ khác như `http://127.0.0.1:9000` → chạy lại với `--old` đó.
- **An toàn khi chạy lại:** mỗi database một transaction (lock timeout 5 s). Chạy lại không đổi thêm gì.

1. Triển khai cấu hình mới trước, để ảnh tải lên sau thời điểm này đã mang URL mới: `OBJECT_STORAGE_PUBLIC_BASE_URL=https://<domain>/media`, rồi `docker compose up -d`.
2. Backup `catalog_db`, `vendor_db`, `review_db`.
3. Chạy thử (không ghi gì):
   `bash deploy/rewrite-media-urls.sh --old http://localhost:9000 --new https://<domain>/media`
   - Cột `rewritten` là số dòng sẽ đổi, `already_new` là số dòng đã đúng, `other` là số dòng không đụng tới.
   - Đọc bảng "URLs left unchanged". Nếu thấy một host cũ khác, chạy thêm lần nữa với `--old` đó.
4. Áp dụng: thêm `--apply` vào lệnh ở bước 3. Chạy lại dry-run; `rewritten` phải bằng 0 ở mọi bảng.
5. Mở vài trang sản phẩm, trang shop và review có ảnh: ảnh tải từ `https://<domain>/media/...`.

- **Rollback:** đảo `--old`/`--new`, thêm `--apply`. Mọi URL của bucket (kể cả ảnh tải sau khi chuyển) trỏ về base cũ, nơi MinIO vẫn phục vụ đủ object. Muốn khôi phục đúng từng byte thì restore backup ở bước 2.
- **Kết nối khác Compose:** `PSQL_CMD="psql -h <host> -U <user>"`. Tên database khác mặc định: `MEDIA_DBS="..."`.

## Triển khai

1. DNS của `SITE_DOMAIN` trỏ về VPS. Cổng 80 phải mở từ Internet để Let's Encrypt xác minh.
2. Điền `.env` như bảng trên, rồi build lại frontend vì `NEXT_PUBLIC_API_BASE_URL` đổi: `bash deploy/build-images.sh frontend`.
3. Chạy `docker compose up -d`.
4. Firewall (ufw): mở 22, 80, 443/tcp và 443/udp, chặn phần còn lại.
5. Chạy `bash deploy/check-exposure.sh`; phải kết thúc bằng `OK: only the edge proxy is public.`

## Smoke

- Từ một máy ngoài VPS:
  - `curl -I http://<domain>/` trả 308 sang `https://`.
  - `curl -I https://<domain>/` trả 200 kèm `Strict-Transport-Security`.
  - `curl https://<domain>/healthz` trả `{"status":"ok"}`.
  - Mở trang chủ, đăng nhập, xem một sản phẩm có ảnh: ảnh tải từ `https://<domain>/media/...`.
- Các truy cập sau phải trả 404:
  - `curl -X PUT https://<domain>/media/product-images/x`.
  - `curl https://<domain>/media/`.
  - `curl https://<domain>/internal/orders/x`.
- Các kết nối sau phải bị từ chối hoặc timeout, nhưng thành công từ chính VPS (`127.0.0.1`):
  - `nc -vz <ip VPS> 5432`.
  - `nc -vz <ip VPS> 6379`, `4222`, `9000`, `8080`, `3000`, `8081`.
- Rate limit theo IP thật: 31 lần `POST /api/auth/login` trong một phút từ một máy thì lần 31 trả 429. Cùng lúc đó, một máy khác vẫn đăng nhập được.

## Vận hành

- **Xem database hoặc NATS từ máy cá nhân:** dùng SSH tunnel, ví dụ `ssh -L 5432:127.0.0.1:5432 <vps>`, không mở cổng.
- **Chứng chỉ:** lưu trong volume `caddy-data`; Caddy tự gia hạn trước hạn 30 ngày.
  - Mất volume thì Caddy xin lại chứng chỉ. Let's Encrypt giới hạn số lần cấp, nên tránh xóa volume nhiều lần.
  - Log `docker compose logs caddy` có `certificate obtained` / `renewed`.
- **Đổi Caddyfile:** `docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile` (không gián đoạn). CI kiểm tra cú pháp và định dạng.
- **Sau mỗi lần đổi compose hoặc `.env`:** chạy lại `bash deploy/check-exposure.sh`.

## Sự cố

| Triệu chứng | Nguyên nhân thường gặp |
|---|---|
| Trang trả 502 từ Caddy | Gateway hoặc frontend chưa chạy, hoặc đã khởi động lại mà không có `compose.edge.yml` (thiếu `COMPOSE_FILE`): `docker compose up -d` lại |
| Không lấy được chứng chỉ | DNS chưa trỏ đúng, cổng 80 bị chặn từ ngoài, hoặc đã chạm giới hạn của Let's Encrypt: xem `docker compose logs caddy` |
| Ai cũng bị 429 cùng lúc | Gateway không tin Caddy (`TRUSTED_PROXY_CIDRS` khác IP của Caddy), nên mọi người chung một IP: kiểm tra `EDGE_PROXY_IP` |
| Ảnh hỏng | `OBJECT_STORAGE_PUBLIC_BASE_URL` chưa là `https://<domain>/media`, hoặc ảnh cũ còn URL `localhost` (chạy `deploy/rewrite-media-urls.sh`) |
| `network ... overlaps` khi `up` | `EDGE_SUBNET` trùng mạng có sẵn: đổi `EDGE_SUBNET` và `EDGE_PROXY_IP` |
| `check-exposure.sh` báo `EXPOSED` | Có biến bind là `0.0.0.0` hoặc có service tự thêm `ports`: sửa rồi `up -d` lại |

## Rollback

Bỏ `deploy/compose.edge.yml` khỏi `COMPOSE_FILE` thì trang web ngừng phục vụ từ ngoài, vì gateway và frontend chỉ nghe `127.0.0.1`. Không có đường "mở lại cổng 8080/3000" an toàn. Nếu Caddy lỗi cấu hình, sửa Caddyfile rồi reload, hoặc quay về bản Caddyfile trước đó.
