# Platform upgrade (PLT-01…05) — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/13-platform.md) và [hợp đồng event](../backend/pkg/events/CONTRACTS.md).

## Thay đổi

| Phần | Trước | Sau |
|---|---|---|
| Event giữa service (12 luồng) | Outbox PostgreSQL rồi gọi HTTP thẳng tới bên nhận | Outbox rồi publish lên NATS JetStream (stream `SHOPEE_EVENTS`, lưu trên đĩa 14 ngày); bên nhận đọc bằng consumer bền, ghi inbox cùng transaction rồi ACK |
| Lỗi ở bên nhận | Bên gửi retry hoặc park trong outbox của mình | Bên nhận retry 5 lần (2 s, 8 s, 32 s, 128 s) rồi park trong `event_inbox`; lỗi vĩnh viễn park ngay. Replay/discard ở `/admin/events`, có lý do và audit |
| Order từ chối kết quả thanh toán/hoàn tiền | HTTP 409, Payment đánh dấu review | Order rollback phần dở, gửi `order.payment_outcome_rejected` qua effect bền, Payment đánh dấu review như cũ |
| Lệnh cần kết quả ngay | API | Vẫn API (giữ/commit/nhả hàng, yêu cầu hoàn tiền, báo giá ship, checkout) |
| Xác thực giữa service | Một khóa chung cho mọi service, mọi route | Mỗi service có tên và khóa riêng; mỗi route nội bộ chỉ cho các service cần gọi (bảng ở module-details) |
| Cổng publish | Mọi service và PostgreSQL/Redis/NATS/MinIO mở trên mọi interface | Mọi cổng nghe 127.0.0.1; production chỉ Caddy (80/443, TLS) public qua `deploy/compose.edge.yml` — xem [edge-runbook](edge-runbook.md) |
| Gateway | Không rate limit, không giới hạn body, không timeout tới service | Rate limit theo IP; upload ≤ 22 MB, body khác ≤ 2 MB, header ≤ 32 KB; xóa header xác thực nội bộ do client gửi; kết nối 5 s, chờ phản hồi 30 s (504); chặn `/internal`, `..`, `%2f`; lỗi 502/504 có `request_id`; production chỉ nhận CORS origin HTTPS public |
| Readiness | Phụ thuộc NATS (và Redis) ở mọi service dù không dùng | Chỉ thành phần service thật sự dùng; NATS mất kết nối không làm service "not ready" (outbox giữ event) |
| Ngân sách tài nguyên | Pool DB mặc định, không giới hạn câu SQL, không `WriteTimeout` | Mỗi service tối đa 8 kết nối DB (`DB_MAX_CONNS`), mỗi câu SQL ≤ 30 s (`DB_STATEMENT_TIMEOUT`), `HTTP_WRITE_TIMEOUT` 60 s |
| NATS | `-js` lưu ở thư mục tạm của container, ai trong mạng Docker cũng publish được | `deploy/nats/nats.conf`: JetStream trên volume `nats-data`; mỗi service đăng nhập bằng khóa riêng, chỉ publish loại event của mình và chỉ đọc consumer của mình |

Rate limit ở gateway (mỗi IP mỗi phút): đăng nhập/đăng ký/reset/refresh 30; checkout 20; webhook 600; upload multipart 60; còn lại 1200. Identity, Payment, Review giữ giới hạn riêng chi tiết hơn.

## Cấu hình mới

| Biến | Ý nghĩa |
|---|---|
| `<SERVICE>_INTERNAL_KEY` (11 biến) | Khóa riêng của từng service. Sinh bằng `bash deploy/gen-service-keys.sh`; mỗi service chỉ nhận khóa của nó qua `INTERNAL_SERVICE_KEY` |
| `INTERNAL_SERVICE_KEYS` | `tên=sha256(khóa)` của mọi service (không bí mật). Khi xoay khóa: `tên=hashmới\|hashcũ` |
| `INTERNAL_AUTH_ACCEPT_SHARED_KEY` | `true` chỉ trong lúc chuyển: chấp nhận khóa chung `IDENTITY_SERVICE_KEY` từ bất kỳ caller nào |
| `EVENT_PUBLISHING` | `jetstream` (mặc định) hoặc `http` (rollback) |
| `INTERNAL_BIND_ADDRESS`, `PUBLIC_BIND_ADDRESS` | Interface publish cổng (mặc định đều `127.0.0.1`) |
| `SITE_DOMAIN`, `ACME_EMAIL`, `EDGE_SUBNET`, `EDGE_PROXY_IP` | Lớp biên Caddy (xem [edge-runbook](edge-runbook.md)) |
| `DB_MAX_CONNS`, `DB_STATEMENT_TIMEOUT`, `HTTP_WRITE_TIMEOUT` | Ngân sách (mặc định 8, 30 s, 60 s) |

Production từ chối khởi động nếu thiếu khóa riêng (trừ khi bật cờ chuyển đổi), và gateway từ chối `ALLOWED_ORIGINS` không phải HTTPS public.

## Thứ tự triển khai

Đây là thay đổi cho mọi service (thư viện chung đổi): build lại cả 13 image.

1. Backup mọi database.
2. Sinh khóa: `bash deploy/gen-service-keys.sh` rồi dán vào `.env` (không commit). Giữ `IDENTITY_SERVICE_KEY`. Đặt `INTERNAL_AUTH_ACCEPT_SHARED_KEY=true`.
3. Đặt `ALLOWED_ORIGINS` đúng origin HTTPS của frontend.
4. Build: `bash deploy/build-images.sh` (toàn bộ).
5. NATS lưu trên đĩa: `docker compose up -d nats`. Trước đây chưa có event nào được publish, nên không mất gì.
6. Migration (tất cả chỉ thêm bảng hoặc mở rộng CHECK): Catalog `000014_event_inbox`, Order `000014_event_inbox` và `000015_rejected_outcome_effect`, Payment `000010_event_inbox`, Shipment `000007_event_inbox`, Notification `000004_event_inbox`.
7. Triển khai toàn bộ backend cùng lúc: `docker compose up -d` (một cửa sổ ngắn; `.env` trên VPS đặt `COMPOSE_FILE` gồm `deploy/compose.edge.yml` theo [edge-runbook](edge-runbook.md)). Service mới gọi service cũ có thể bị 403 trong vài giây; outbox tự gửi lại, người dùng có thể gặp lỗi tạm và thử lại.
8. Kiểm tra (mục Smoke). Sau đó đặt `INTERNAL_AUTH_ACCEPT_SHARED_KEY=false` và `docker compose up -d` lần nữa.
9. Lớp biên theo [edge-runbook](edge-runbook.md), rồi `bash deploy/check-exposure.sh` phải báo OK. Firewall VPS (ufw) chỉ mở 22/80/443, nhưng không thay được bước kiểm tra: Docker tự mở cổng đã publish, vượt qua ufw.

## Smoke

- `docker compose ps`: mọi service healthy (kể cả khi tạm dừng NATS: `docker compose stop nats` thì service vẫn healthy, outbox tăng; `start` lại thì xả hết).
- Đặt và thanh toán một đơn thử: đơn chuyển `paid`, có vận chuyển (Shipment nhận `order.fulfillment_ready`), có email `order_paid` (Notification nhận `order.notification_requested`).
- Duyệt một shop thử: Catalog/Order thấy trạng thái mới (`vendor.status_changed`); chủ shop nhận email.
- `curl -s localhost:8222/jsz?streams=1`: có stream `SHOPEE_EVENTS`, số message tăng, consumer theo dõi đúng.
- `/admin`: ô "Events a service could not apply" bằng 0; `/admin/events` trống.
- Gọi trực tiếp một route nội bộ bằng khóa của service khác (ví dụ khóa Review gọi `POST /internal/orders/<id>/mark-paid` của Order) → 403. Gọi `https://<domain>/internal/...` qua gateway → 404.
- 31 lần `POST /api/auth/login` trong một phút từ một IP → lần 31 trả 429 có `Retry-After`.

## Theo dõi

| Chỉ số / log | Ý nghĩa | Ngưỡng gợi ý |
|---|---|---|
| Ô "Events a service could not apply", `GET /api/<svc>/admin/events/stats` (`events_parked`, `events_oldest_parked_seconds`) | Event bên nhận không áp dụng được | > 0: xem `/admin/events` |
| `event_bus_connected` trong stats | Service đang nối NATS | 0 kéo dài: kiểm tra NATS |
| Outbox của bên gửi (ô "Background jobs" sẵn có, `vendor_status_propagation_pending`, `catalog_status_dispatch_failed`, `order_effect_retry`, `payment_order_sync`…) | Event chưa publish được | Tăng khi NATS ngừng; phải về 0 sau khi NATS lên |
| `event_parked`, `event_retry`, `event_consumer_unavailable`, `event_fetch_failed` | Log consumer | `event_parked` cần người xử lý |
| `order_outcome_rejected_reported` | Order từ chối kết quả thanh toán/hoàn tiền | Có thì xem review ở `/admin/payments` |
| Gateway `rate_limited` (429), `upstream_timeout` (504), `upstream_proxy_error` | Biên | 504 tăng: service chậm |

## Sự cố

- **NATS ngừng:** web, checkout, thanh toán vẫn chạy. Event nằm trong outbox của bên gửi và tự gửi khi NATS lên lại. Trong lúc đó: trạng thái shop/sản phẩm đồng bộ chậm (Catalog/Order còn vòng đối soát 15 giây với Vendor), vận chuyển/email/quyết toán chờ. Không cần thao tác.
- **Event bị park:** xem `last_error` ở `/admin/events`, sửa nguyên nhân (ví dụ đơn chưa tồn tại, dữ liệu sai), rồi Replay với lý do; Discard chỉ khi event sai hoặc lỗi thời. Không xóa tay trong `event_inbox`.
- **Payment báo "order_rejected_outcome":** Order đã từ chối khoản thanh toán/hoàn tiền (sai số tiền, đơn đã hủy). Xử lý ở `/admin/payments` như trước.
- **Lộ khóa của một service:** sinh khóa mới cho service đó, thêm hash mới vào `INTERNAL_SERVICE_KEYS` (`tên=hashmới|hashcũ`), khởi động lại mọi service và `nats` (khóa cũng là mật khẩu event bus), rồi bỏ hash cũ và khởi động lại lần nữa.
- **`event_bus_error` "Authorization Violation" / `event_bus_disconnected` lặp lại:** mật khẩu service khác mật khẩu NATS đang giữ. NATS chỉ đọc mật khẩu lúc khởi động, nên sau khi đổi `<SERVICE>_INTERNAL_KEY` phải chạy lại cả `nats`: `docker compose up -d nats <service>`. Outbox giữ event trong lúc đó.
- **`event_bus_error` "Permissions Violation":** service publish loại event không thuộc về nó, hoặc dùng consumer chưa được khai báo trong `deploy/nats/nats.conf`. Đây là lỗi code hoặc cấu hình (CI có test đối chiếu); event nằm trong outbox cho tới khi sửa.
- **403 "This service may not call this route" sau khi thêm tính năng:** route mới cần khai báo service được phép gọi (`Allow(...)` trong router của bên nhận).

## Rollback

- **Event:** đặt `EVENT_PUBLISHING=http` rồi khởi động lại. Bên gửi gọi lại route HTTP nội bộ cũ (vẫn còn, có xác thực). Bên nhận tiếp tục đọc stream nên event đã publish vẫn được áp dụng. Không xóa stream hay inbox.
- **Xác thực:** đặt `INTERNAL_AUTH_ACCEPT_SHARED_KEY=true`. Image cũ (dùng khóa chung) được image mới chấp nhận.
- **Image cũ:** đọc được schema mới (chỉ thêm bảng). Effect `report_rejected_outcome` còn tồn sẽ không chạy được với image Order cũ: xử lý trước khi rollback Order (Payment review tay).
- **Migration down:** `event_inbox` từ chối down khi còn event park hoặc đã có audit; Order `000015` down lỗi khi còn effect `report_rejected_outcome`.
