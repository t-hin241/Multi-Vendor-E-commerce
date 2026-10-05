# Load test (OPS-06)

Đo tải để tìm điểm nghẽn, không phải để "chạy cho có số". Kết quả và cách đọc: [17-observability-capacity](../../docs/module-details/17-observability-capacity.md). Dashboard và cảnh báo: [observability-runbook](../observability-runbook.md).

## Môi trường

- Project Compose riêng `shopee-loadtest`, có volume riêng. Stack dev và dữ liệu của nó không bị đụng tới.
- Secret được sinh mới cho mỗi môi trường (`deploy/gen-service-keys.sh` và `openssl`), lưu ngoài repo ở `$LOADTEST_STATE_DIR`. `.env` của bạn không được đọc.
- Dữ liệu là bản sao của volume PostgreSQL dev **đang dừng**. Nguồn được mount chỉ đọc; trên bản sao, mọi role được đặt lại mật khẩu mới.
- Giả lập VPS 4 vCPU / 8 GB:
  - toàn bộ stack (kể cả observability) chạy trên CPU 0–3, với giới hạn RAM như production (`docker-compose.yml`);
  - k6 chạy trên CPU 4–7, nên không lấy CPU của thứ nó đang đo.
- Tải đi qua gateway như trình duyệt: cùng header CSRF/Origin, cùng rate limit. k6 có IP cố định mà gateway tin cho `X-Forwarded-For`, và mỗi virtual user gửi địa chỉ riêng, nên mỗi người mua giả lập có hạn mức riêng. Không có giới hạn nào bị nới.
- Fixture **chỉ trên bản sao** (`run.sh fixture`):
  - mọi shop đã duyệt có phương thức giao `TEST-EXP` (dữ liệu dev chỉ có một shop giao được hàng);
  - sản phẩm chưa có khối lượng đóng gói nhận 500 g;
  - tồn kho +100.000 mỗi mục, có ghi `stock_movements`. Tồn kho seed chỉ ~10 mỗi mục, vài trăm đơn là cạn, và lúc đó k6 đo "hết hàng" thay vì đo hệ thống.
- Mỗi lượt của một virtual user dùng một trong 64 địa chỉ của nó. Một virtual user mua nhiều lần mỗi phút, nếu chỉ một địa chỉ thì sẽ chạm rate limit checkout 20/phút/IP.

## Chạy

```bash
export LOADTEST_STATE_DIR=~/.cache/shopee-loadtest      # tùy chọn
bash deploy/loadtest/run.sh copy-db shopee_postgres-data   # stack dev phải đang dừng
bash deploy/loadtest/run.sh up                             # build, migrate, khởi động
bash deploy/loadtest/run.sh fixture

# Kịch bản (kết quả JSON trong deploy/loadtest/results/, không commit):
bash deploy/loadtest/run.sh k6 browse.js   -e RUN_ID=b1 -e START_RATE=2 -e STEP_RATE=4 -e STEPS=6
bash deploy/loadtest/run.sh k6 checkout.js -e RUN_ID=c1 -e START_RATE=1 -e STEP_RATE=1 -e STEPS=6 -e LINES=3 -e VENDORS=2
bash deploy/loadtest/run.sh k6 mixed.js    -e RUN_ID=m1 -e START_RATE=5 -e STEP_RATE=5 -e STEPS=6

bash deploy/loadtest/run.sh down                           # xóa container và volume của môi trường tải
```

- Tham số tải: `START_RATE`, `STEP_RATE`, `STEPS`, `STEP_DURATION`, tính theo lượt/giây. Đây là tải mở: tốc độ không tự giảm khi hệ thống chậm, nên điểm gãy hiện ra rõ.
- `LINES`/`VENDORS`: số dòng và số shop trong giỏ khi checkout. `NO_THINK=1` bỏ thời gian nghỉ giữa các bước.
- `DEBUG=1` in phản hồi lỗi (envelope lỗi: mã, thông điệp, request id).
- Muốn trace mọi request khi chẩn đoán: `LOADTEST_TRACE_RATIO=1` trước `up` lần đầu. Khi đo năng lực, giữ 0.1 vì trace 100% tự tốn tài nguyên.
- Sau mỗi lần chạy: `bash deploy/loadtest/run.sh compose exec -T postgres ...` để kiểm tra đúng/sai nghiệp vụ (oversell, trùng tiền), xem báo cáo.

## Tiêm lỗi

`bash deploy/loadtest/run.sh fault <service> <giây>` dừng một service (hoặc `nats`) trong lúc đang chạy tải, rồi bật lại. Xem độ trễ phục hồi ở dashboard Platform → Events (outbox, delivery lag).

## Lưu ý khi đọc số

- Docker Desktop (WSL2) trên máy dev có fsync ~100 ms, so với ~1 ms trên NVMe của VPS (`pg_test_fsync`). Vì vậy `up` đặt `synchronous_commit=off` trên bản sao; muốn đo kèm đĩa thật thì đặt `LOADTEST_SYNCHRONOUS_COMMIT=on`. Chi phí theo số commit được báo bằng số commit trên mỗi request.
- Công việc trên host (build, IDE) dùng chung CPU với VM Docker: đừng làm việc nặng trong lúc đo.
- Muốn đo "nhàn rỗi" thì đợi outbox về 0 (dashboard Platform → Outbox). Ngay sau một đợt tải, việc nền còn đang chạy.
