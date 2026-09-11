# Production Checklist

## Mục Tiêu

Checklist này dùng trước khi đưa MVP microservices lên production bằng Docker trên VPS. Mục tiêu là giảm rủi ro phổ biến: mất dữ liệu, lộ secret, deploy lỗi, không thấy lỗi production, message/event lỗi và payment webhook xử lý sai.

## Configuration

- Tất cả cấu hình production đọc từ environment variables.
- Không commit `.env`, private key hoặc provider secret.
- Có `.env.example` chỉ chứa tên biến và giá trị mẫu an toàn.
- Tách rõ config local, staging và production.
- Bật strict CORS cho domain production.
- Thiết lập timezone và format thời gian thống nhất, ưu tiên UTC trong database.

## Security

- Bắt buộc HTTPS.
- Password được hash bằng bcrypt hoặc argon2.
- Access token ngắn hạn.
- Refresh token có thể revoke.
- Rate limit cho login, register, password reset, checkout và webhook.
- Validate input ở mọi public endpoint.
- Validate upload: content type, file size, image dimensions.
- Không serve file upload trực tiếp từ app container nếu dùng object storage.
- Admin/vendor endpoint kiểm tra quyền ở use case.
- Payment webhook verify signature.
- Không log password, token, card data hoặc secret.

## Database

- Migration của từng service chạy có kiểm soát trước khi service version mới nhận traffic.
- Có backup PostgreSQL tự động hằng ngày.
- Có retention policy cho backup.
- Đã test restore ít nhất một lần.
- Có index cho query quan trọng: product listing, order lookup, vendor order, payment event id.
- Database user production không dùng quyền superuser nếu không cần.
- Service không đọc/ghi trực tiếp schema hoặc database của service khác.

## Redis

- Xác định dữ liệu Redis nào được phép mất.
- Nếu dùng Redis cho queue, cấu hình persistence phù hợp.
- Đặt password hoặc bind private network.
- Theo dõi memory usage.
- Có TTL cho cache/session phù hợp.

## Message Broker

- Message broker production có authentication hoặc private network.
- Queue/topic cho domain events được đặt tên rõ theo service hoặc bounded context.
- Consumer xử lý event idempotent.
- Có retry policy và dead-letter queue hoặc parking queue cho event lỗi.
- Theo dõi event lag, queue depth và consumer failure.

## Payment

- Dùng key production riêng với key sandbox.
- Webhook endpoint dùng HTTPS public URL.
- Webhook secret được cấu hình riêng.
- Provider event id được lưu unique.
- Webhook handler idempotent.
- Có log correlation giữa order id, payment id và provider event id.
- Có flow xử lý payment failed, cancelled, refunded.
- Có test thanh toán thật với số tiền nhỏ trước khi mở public.

## Docker And Deployment

- Dockerfile production không dùng hot reload.
- Image chạy bằng non-root user nếu khả thi.
- Mỗi service có health check endpoint.
- Docker Compose production có restart policy.
- Reverse proxy bằng Caddy hoặc Nginx.
- TLS hoạt động và tự động gia hạn nếu dùng Caddy/Let's Encrypt.
- Có script deploy hoặc GitHub Actions workflow rõ ràng.
- Có rollback path về image/tag trước đó.
- Không mount source code production nếu không cần.
- Deploy nhiều service phải có thứ tự rõ khi thay đổi API/event contract.

## CI/CD

- CI chạy lint và test cho backend.
- CI chạy test/build cho frontend nếu frontend nằm cùng repo.
- CI build Docker image.
- Deploy chỉ chạy từ branch/tag được chọn.
- Secrets chỉ lưu trong GitHub Actions hoặc trên VPS.
- Có manual approval cho production deploy nếu team cần kiểm soát.

## Observability

- Log dạng JSON hoặc format parse được.
- Mỗi request có request id.
- Error response không lộ stack trace cho user.
- Sentry hoặc công cụ tương đương nhận lỗi backend/frontend.
- Monitoring CPU, memory, disk, network.
- Uptime monitoring từ bên ngoài.
- Alert khi disk gần đầy, app restart liên tục hoặc payment webhook lỗi tăng.
- Trace hoặc log correlation phải đi xuyên qua API Gateway và các services bằng request id/correlation id.

## Smoke Test Sau Deploy

Chạy sau mỗi lần deploy production:

- Mở storefront.
- Đăng nhập buyer.
- Đăng nhập vendor.
- Admin duyệt vendor hoặc kiểm tra vendor đã duyệt.
- Vendor tạo hoặc cập nhật sản phẩm test.
- Buyer thêm sản phẩm vào cart.
- Buyer checkout.
- Payment sandbox hoặc payment test flow trả kết quả thành công.
- Webhook cập nhật order sang paid.
- Vendor thấy order mới.
- Admin thấy order trong dashboard.
- Event giữa Payment, Order, Inventory và Notification được consume thành công.

## Backup And Recovery

- Backup database tự động.
- Backup được lưu ngoài VPS nếu có thể.
- Có tài liệu restore.
- Định kỳ thử restore trên môi trường staging/local.
- Object storage có lifecycle policy hoặc backup nếu dữ liệu quan trọng.

## Performance Readiness

- Có connection pool database hợp lý.
- Có timeout cho HTTP server, database query và provider calls.
- Có timeout cho request service-to-service.
- Có pagination cho danh sách sản phẩm, đơn hàng, vendor.
- Không trả danh sách lớn không giới hạn.
- Cache endpoint read-heavy sau khi đo được nhu cầu.

## Go-Live Criteria

- Critical user flow chạy ổn: vendor đăng sản phẩm, buyer mua hàng, payment webhook cập nhật order.
- Các service cốt lõi giao tiếp ổn qua API/event contract.
- Admin có thể kiểm duyệt và xử lý sự cố cơ bản.
- Backup và restore đã được kiểm chứng.
- Monitoring và error tracking đã hoạt động.
- Secrets production không nằm trong repository.
- Có rollback plan rõ ràng.
