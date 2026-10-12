# PW-020 — tải và lỗi chuỗi authorize

Chạy trên **bản sao riêng `shopee-loadtest`**, sau khi cập nhật image và migration
bằng `run.sh up`. Không chạy probe/fixture trên staging dùng chung hoặc production.
`LOADTEST_STATE_DIR` trỏ tới cấu hình loadtest hiện có; không dùng `.env` của ứng dụng.
Xem [môi trường đo](README.md) và [runbook staff](../shop-staff-runbook.md).
Kết quả đã chạy: [release record ngày 2026-10-12](reports/pw020-2026-10-12.md).

## Phạm vi và ngưỡng trước khi đo

- PostgreSQL/Vendor/Identity thật, resource budget của Compose, CPU 0–3.
- k6 và probe nằm CPU 4–7. Probe dùng **chính `pkg/shopaccess.Client`** của source
  đang đo, không mô phỏng quyết định cấp quyền. Hai reverse proxy chỉ thêm độ trễ;
  trạng thái tài khoản và membership vẫn được đọc từ service/database thật.
- 32 shop giả, 32 owner và 32 buyer staff, chỉ staff có `orders.read`. Không tạo
  tài khoản đăng nhập được, gửi mail, tạo đơn hoặc giao dịch tiền. Invitations paused.
- `TARGET=vendor`: gọi endpoint nội bộ Vendor → Identity; `TARGET=client`: gọi
  client → Vendor → Identity, gồm deadline 3 giây và chuyển lỗi thành 503.
  Đây là phép đo chuỗi authorize, chưa phải nghiệm thu seller UI/session/gateway.
- Healthy: p95 < 250 ms, p99 < 500 ms, khả dụng > 99,9%, đúng kết quả 100%,
  không dropped iterations. Các mức tải 20/40/80 request/s, rồi soak 40 request/s.
- Identity thêm 500 ms: p95 < 1.000 ms, p99 < 1.500 ms, vẫn cấp đúng quyền.
- Identity thêm 2.500 ms: 100% 503 `authorization_unavailable`, p95 < 2.250 ms,
  p99 < 2.500 ms (client Identity timeout 2 giây).
- Vendor thêm 3.500 ms qua probe: 100% 503, p95 < 3.250 ms, p99 < 3.500 ms
  (deadline client 3 giây). Không request nào nhận grant khi kết quả không rõ.
- Stop Identity/Vendor thật: 100% 503 đúng mã; đo lại healthy sau restart.
  Dùng cùng ngân sách lỗi như timeout: Identity p95/p99 < 2.250/2.500 ms,
  Vendor p95/p99 < 3.250/3.500 ms (bao gồm khả năng DNS/kết nối chưa thất bại ngay).
  Phục hồi: grant đầu tiên qua client trong < 5 giây tính từ lệnh start;
  số đo gồm overhead Docker CLI và thăm dò, là cận trên chứ không phải thời
  điểm service nhận traffic chính xác.
  503 chủ động trong drill không được tính là availability đạt healthy.
- `finance.read` của staff: 100% từ chối (Vendor trả 200 `allowed=false`, client
  trả 403 `permission_denied`); không trộn vào lỗi hạ tầng.

Đây là ngưỡng nghiệm thu kỹ thuật cho workload này. Ghi tải được chấp nhận và
giới hạn môi trường trong release record; không suy thành SLO của toàn marketplace.

## Lệnh

```bash
# Môi trường đã có bản sao DB, không copy lại hay xóa volume.
export LOADTEST_STATE_DIR=/path/to/existing/loadtest-state
LOADTEST_SYNCHRONOUS_COMMIT=on bash deploy/loadtest/run.sh up
bash deploy/loadtest/authorize.sh prepare

bash deploy/loadtest/authorize.sh k6 -e RUN_ID=healthy20 -e RATE=20
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=healthy40 -e RATE=40
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=healthy80 -e RATE=80
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=soak -e TARGET=client -e RATE=40 -e DURATION=5m
bash deploy/loadtest/authorize.sh metrics healthy

bash deploy/loadtest/authorize.sh k6 -e RUN_ID=slow -e TARGET=client -e RATE=20 -e DURATION=30s -e IDENTITY_DELAY_MS=500 -e P95_MS=1000 -e P99_MS=1500
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=identity-timeout -e TARGET=client -e RATE=20 -e DURATION=30s -e IDENTITY_DELAY_MS=2500 -e EXPECT=unavailable -e P95_MS=2250 -e P99_MS=2500
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=vendor-timeout -e TARGET=client -e RATE=20 -e DURATION=30s -e VENDOR_DELAY_MS=3500 -e EXPECT=unavailable -e P95_MS=3250 -e P99_MS=3500

# Chạy từng service; luôn start lại kể cả khi k6 không đạt ngưỡng.
bash deploy/loadtest/authorize.sh stop identity
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=identity-down -e TARGET=client -e RATE=20 -e DURATION=30s -e EXPECT=unavailable -e P95_MS=2250 -e P99_MS=2500
bash deploy/loadtest/authorize.sh recover identity
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=identity-recovered -e TARGET=client -e RATE=40 -e DURATION=1m

bash deploy/loadtest/authorize.sh stop vendor
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=vendor-down -e TARGET=client -e RATE=20 -e DURATION=30s -e EXPECT=unavailable -e P95_MS=3250 -e P99_MS=3500
bash deploy/loadtest/authorize.sh recover vendor
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=vendor-recovered -e TARGET=client -e RATE=40 -e DURATION=1m
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=denied-direct -e TARGET=vendor -e EXPECT=denied -e RATE=20 -e DURATION=30s
bash deploy/loadtest/authorize.sh k6 -e RUN_ID=denied -e TARGET=client -e EXPECT=denied -e DURATION=30s

bash deploy/loadtest/authorize.sh restore
```

Probe không publish port, không log header/key/body. Các secret chỉ đến từ cấu hình
loadtest; `prepare` sinh key lời mời tạm trong môi trường process. Không commit file
secret. `restore` trả Vendor về route Identity và feature flags trong env gốc, dừng
probe, giữ volume. Nếu stack ban đầu dừng, dừng lại bằng `run.sh compose stop`.

JSON trong `results/authorize-*.json` chứa percentile, số mẫu, availability,
correctness và kết quả threshold. Lưu thêm metrics trước/sau và resource usage;
raw artifact không commit, release record phải có kết quả tổng hợp, commit nguồn,
ngày UTC, cờ, cấu hình timeout/pool, giới hạn phép đo và kết quả phục hồi.
