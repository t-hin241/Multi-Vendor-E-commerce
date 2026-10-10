# Observability và ngân sách tài nguyên — triển khai và vận hành

Liên quan: [17-observability-capacity](../docs/module-details/17-observability-capacity.md) (số đo, điểm nghẽn), [loadtest](loadtest/README.md), [edge-runbook](edge-runbook.md).

## Có gì

| Phần | Ở đâu | Ghi chú |
|---|---|---|
| Metrics mỗi service | `:9464/metrics` trong mạng Docker (`pkg/telemetry`) | Không đi qua gateway hay Caddy |
| RED theo route | `http_server_request_duration_seconds{service,route,method,code}` | `route` là template (`/api/orders/:id`), không phải path thật |
| Gọi giữa service, gọi provider | `http_client_request_duration_seconds{service,peer,...}` | Dùng chung một pool kết nối mỗi process |
| Pool PostgreSQL | `db_pool_*` | Chờ kết nối = `db_pool_empty_acquire_wait_seconds_total` |
| Outbox | `outbox_pending_events`, `outbox_oldest_pending_seconds` | Đọc lúc scrape, mỗi service một danh sách (`repository/backlog_metrics.go`) |
| Event bus | `eventbus_publish_*`, `eventbus_handle_*`, `eventbus_delivery_lag_seconds` | Độ trễ đo từ lúc sự kiện xảy ra đến lúc consumer bắt đầu xử lý |
| Trace | Tempo, OTLP/HTTP `tempo:4318` | Span ghi route, method, status, peer, câu SQL; **không** ghi query string, header, body, tham số SQL |
| Container, host | cAdvisor, node-exporter | |
| PostgreSQL, Redis, NATS | postgres-exporter (`pg_stat_statements`), redis-exporter, nats-exporter | |
| Dashboard | Grafana, thư mục "Shopee" | Platform, Resources and budgets, Database |
| Cảnh báo | `deploy/observability/alerts.yml` | Hiện trong Prometheus/Grafana; **chưa có kênh gửi** (xem cuối trang) |

## Bật trên VPS

1. Thêm vào `.env` (không commit):
   - `COMPOSE_FILE=docker-compose.yml:deploy/compose.edge.yml:deploy/observability/compose.observability.yml`
   - `OTEL_EXPORTER_OTLP_ENDPOINT=http://tempo:4318`
   - `GRAFANA_ADMIN_PASSWORD` và `MONITOR_DB_PASSWORD`: chuỗi ngẫu nhiên, ví dụ `openssl rand -hex 24`.
2. PostgreSQL cần khởi động lại để nạp `pg_stat_statements` và cấu hình bộ nhớ mới: `docker compose up -d postgres`. Làm trong khung bảo trì, vì các service mất DB vài giây.
3. Tạo role thống kê chỉ đọc: `bash deploy/observability/monitor-role.sh`. Chạy lại sau khi restore hoặc đổi mật khẩu.
4. `docker compose up -d`.
5. Mở Grafana qua SSH tunnel: `ssh -L 3001:127.0.0.1:3001 <vps>` rồi vào `http://localhost:3001` (user `admin`). Prometheus cũng vậy: `-L 9090:127.0.0.1:9090`.
6. Kiểm tra: Prometheus → Status → Targets đều `up` (12 service và các exporter). `bash deploy/check-exposure.sh` vẫn chỉ thấy 80/443 public.

**Tắt:** bỏ file observability khỏi `COMPOSE_FILE` và để trống `OTEL_EXPORTER_OTLP_ENDPOINT`. Service vẫn mở `/metrics` nội bộ, không ảnh hưởng gì.

**Rollback code:** image cũ không có `/metrics`, nên Prometheus báo target down; không ảnh hưởng nghiệp vụ.

## Ngân sách (VPS 4 vCPU / 8 GB)

Giới hạn khai báo trong compose; số đo thực tế và lý do chọn ở báo cáo 17.

| Thành phần | RAM giới hạn | Khác |
|---|---|---|
| PostgreSQL | 2 GB | `shared_buffers` 512 MB, `effective_cache_size` 1.5 GB, `work_mem` 8 MB, `max_connections` 100, câu > 500 ms ghi log **không kèm tham số** |
| Catalog | 160 MB | `GOMEMLIMIT` 136 MiB, `DB_MAX_CONNS` 24 |
| 10 service còn lại + gateway | 128 MB mỗi cái | `GOMEMLIMIT` 108 MiB; `DB_MAX_CONNS`: order 10, identity/inventory/cart 8, vendor/payment 6, shipment/notification/review 4, admin 2 |
| Frontend | 512 MB | Heap V8 384 MB |
| Redis (cache) | 96 MB | `maxmemory` 64 MB, `allkeys-lru`, không persistence: bộ đếm rate limit; cảnh báo khi evict kéo dài |
| Redis (queue) | 192 MB | `maxmemory` 128 MB, `noeviction`, AOF: job notification; đầy thì enqueue lỗi, cảnh báo ở 80% |
| NATS | 256 MB | JetStream tối đa 2 GB đĩa; stream 1 GB (cảnh báo ở 80%) |
| MinIO | 256 MB | |
| Caddy | 128 MB | |
| Prometheus | 512 MB | Giữ 15 ngày hoặc 4 GB |
| Tempo | 512 MB | Giữ 72 giờ. Không có giới hạn theo dung lượng: ~130 MB/giờ ở 100 request/s với lấy mẫu 10% |
| Grafana | 256 MB | |
| Exporter | 160 + 4 × 48–64 MB | |
| Log container | 3 × 10 MB mỗi container | Xoay vòng, không thể làm đầy đĩa |

Tổng giới hạn ≈ 6,6 GB, còn ~1,4 GB cho OS và page cache. Mỗi giới hạn = RSS đỉnh đo được khi quá tải × 1,3. Kết nối DB của service cộng lại 84; đổi một service thì kiểm lại tổng.

Không đặt giới hạn CPU: throttling sẽ che mất chỗ đang tốn thời gian. Đổi một giới hạn RAM thì đổi `GOMEMLIMIT` của nó theo (≈ 85%).

## Đọc khi có sự cố

- **Chậm:** Platform → "Slowest routes", rồi "Outbound call p95" để biết thời gian nằm ở service nào. Mở Tempo, tìm `service.name` và `name = "POST /api/orders/checkout"` để xem từng span. Chỉ ~10% request có trace.
- **Lỗi 5xx:** từ log gateway có `request_id`. Dòng log của request được trace có thêm `trace_id`.
- **Đơn chưa chuyển trạng thái:** Platform → Outbox và "Event delivery lag". Event bị park: admin → parked events.
- **DB:** Database → "Time waiting for a connection" (pool thiếu) và "Statements by total time" (câu nặng). Pool thiếu thì xem lại tổng `DB_MAX_CONNS` trước khi tăng.
- **Hết RAM / restart:** Resources → "Memory used / limit" và "Restarts".

## Hàng việc cần người xử lý (PW-008)

Mỗi service tự khai báo các hàng việc của các tính năng AF trong `internal/repository/work_queues.go`. Prometheus đọc chúng mỗi lần scrape, mỗi truy vấn giới hạn 2 giây. Metric:

- `work_queue_attention_items{queue, severity}`: số việc **đã quá ngưỡng của chính hàng đó**;
- `work_queue_oldest_attention_seconds`: tuổi của việc cũ nhất;
- `work_queue_probe_success`: 1 khi đọc được hàng.

Alert ở `alerts.yml`, nhóm `work_queues`:

- `WorkQueueCritical`: hàng tiền/kho có việc, kéo dài 10 phút;
- `WorkQueueWarning`: hàng khác có việc, kéo dài 30 phút;
- `WorkQueueUnreadable`: không đọc được hàng.

| Service | Hàng (mức) | Xử lý |
|---|---|---|
| Order | `order_effects_parked` (critical) | Admin → effect parked: sửa nguyên nhân rồi retry |
| Order | `settlement_holds_needs_review`, `settlement_holds_preparing_1h` (critical) | `order-runbook.md` (sổ hold): kiểm Payment; claim chưa chuyển thì Cancel payout item |
| Order | `cancellations_stuck` (critical) | `paid-cancellation-runbook.md` |
| Order | `delivery_exceptions_stuck` (critical) | `delivery-exception-runbook.md` |
| Order | `return_shipping_review` (warning) | `return-shipping-runbook.md` |
| Order | `buyer_notices_review` (warning) | Thông báo cho yêu cầu hỗ trợ không mã đơn không tới được Notification: sửa event bus/Notification rồi đặt lại `requires_review = false, attempts = 0` cho các dòng đó trong `order_buyer_notices` (Payment: `payment_buyer_notices`) |
| Order, Payment, Shipment | `case_sla_overdue` (warning) | `/admin/work-items` (`case-sla-runbook.md`) |
| Payment | `manual_refund_unknown`, `manual_refund_submitted_24h` (critical) | `manual-refund-runbook.md`: đối chiếu sao kê |
| Payment | `refund_destination_unverified_24h`, `approvals_expiring` (warning) | Xác minh tài khoản; duyệt hoặc từ chối yêu cầu trước khi hết hạn |
| Payment | `settlement_holds_payout_claimed` (critical) | Hold đến sau claim: Cancel payout item nếu chưa chuyển, hoặc ghi khoản phải thu |
| Payment | `outcome_sync_review` (critical), `vendor_notices_review`, `buyer_notices_review` (warning) | Kết quả thanh toán/hoàn tiền, thông báo payout hoặc thông báo tài khoản hoàn tiền cho buyer không tới được Order/Notification: admin → review |
| Shipment | `return_shipments_stale` (warning) | Kiện trả đi quá 10 ngày: hỏi hãng |
| Notification | `vendor_actions_need_review`, `notifications_parked` (warning) | `vendor-action-notices-runbook.md`, `notification-runbook.md` |
| Vendor | `staff_invitations_parked`, `return_destinations_unverified_24h` (warning) | `shop-staff-runbook.md`, `return-shipping-runbook.md` |

Thêm một hàng mới:

1. Khai báo truy vấn đã có sẵn ngưỡng, ví dụ `... AND updated_at < now() - interval '24 hours'`.
2. Test `TestWorkQueuesReadOnTheSchema` của service phải đạt.
3. Thêm một dòng vào bảng trên.

## Còn thiếu

- Kênh gửi cảnh báo thật (Alertmanager → email/Telegram) và lịch trực: cần chọn kênh và người nhận.
- Error tracking frontend/backend (Sentry hoặc tương đương) và uptime check từ bên ngoài (OPS-04).
