# Roadmap Phases

## Tổng Quan

Roadmap được chia theo hướng đi từ codebase trống đến production MVP, rồi mới mở rộng marketplace. Mục tiêu là luôn có một hệ thống chạy được sau mỗi phase, tránh tích lũy quá nhiều phần dang dở.

## Phase 0: Foundation

Mục tiêu: khởi tạo nền móng kỹ thuật và quy chuẩn làm việc.

Việc cần làm:

- Khởi tạo Git repository.
- Tạo cấu trúc backend Golang theo microservices tối giản.
- Xác định service boundary ban đầu: Identity, Vendor, Catalog, Inventory, Cart, Order, Payment, Shipment, Admin/BFF, Notification.
- Thiết lập API Gateway hoặc Backend-for-Frontend cho public API.
- Chọn framework HTTP: Gin hoặc Echo.
- Thiết lập PostgreSQL, Redis và message broker bằng Docker Compose.
- Thêm migration tool như Goose, Atlas hoặc golang-migrate.
- Thiết lập cấu hình bằng environment variables.
- Tạo health check endpoint.
- Tạo logging có cấu trúc.
- Thiết lập lint, format, test command.
- Thiết lập CI cơ bản bằng GitHub Actions.

Kết quả hoàn thành:

- Developer chạy được hệ thống local bằng một lệnh Docker Compose.
- Các service nền tảng kết nối được database/cache/message broker.
- CI chạy được test/lint cơ bản.

## Phase 1: Marketplace Core

Mục tiêu: tạo luồng mua bán cơ bản.

Việc cần làm:

- Auth: register, login, refresh token, password reset.
- Role-based access control cho buyer, vendor, admin.
- Vendor onboarding với trạng thái pending, approved, rejected.
- Product management cho vendor.
- Category/catalog cơ bản.
- Upload ảnh sản phẩm lên S3-compatible storage.
- Storefront API cho danh sách sản phẩm, chi tiết sản phẩm, tìm kiếm cơ bản.
- Cart API.
- Checkout API tạo order draft hoặc pending order.

Kết quả hoàn thành:

- Vendor được duyệt có thể đăng sản phẩm.
- Buyer có thể thêm sản phẩm vào giỏ hàng và bắt đầu checkout.
- Admin có thể duyệt vendor và sản phẩm.

## Phase 2: Order, Payment, Shipment

Mục tiêu: hoàn thiện luồng đơn hàng có thanh toán.

Việc cần làm:

- Thiết kế order lifecycle: pending, paid, processing, shipped, completed, cancelled, refunded.
- Thiết kế payment lifecycle: pending, authorized, captured, failed, refunded.
- Tích hợp Stripe hoặc PayPal qua adapter.
- Xử lý payment webhook idempotent.
- Tách order theo vendor nếu cart có nhiều vendor.
- Trừ hoặc giữ tồn kho khi checkout.
- Shipment state cơ bản: pending, ready_to_ship, shipped, delivered.
- Vendor cập nhật trạng thái xử lý đơn.
- Admin xem và can thiệp đơn khi cần.

Kết quả hoàn thành:

- Buyer thanh toán thành công và hệ thống cập nhật đơn chính xác.
- Vendor thấy đơn hàng cần xử lý.
- Webhook thanh toán không tạo dữ liệu trùng khi provider gửi lặp.

## Phase 3: Vendor Operations

Mục tiêu: giúp vendor vận hành và giúp marketplace tính phí.

Việc cần làm:

- Commission rule cơ bản theo phần trăm.
- Tính revenue, commission, vendor balance.
- Vendor dashboard: đơn hàng, doanh thu, sản phẩm bán chạy.
- Export CSV cơ bản cho đơn hàng.
- Notification qua email cho order/payment/vendor status.
- Admin dashboard có bộ lọc đơn, vendor, product.

Kết quả hoàn thành:

- Vendor xem được hiệu quả bán hàng cơ bản.
- Marketplace tính được commission.
- Admin có công cụ vận hành tối thiểu.

## Phase 4: Production Hardening

Mục tiêu: sẵn sàng chạy production trên Docker VPS.

Việc cần làm:

- Thiết lập Dockerfile production.
- Thiết lập Docker Compose production.
- Reverse proxy bằng Caddy hoặc Nginx.
- HTTPS tự động hoặc chứng chỉ TLS được quản lý.
- Backup PostgreSQL tự động.
- Redis persistence hoặc cấu hình phù hợp với dữ liệu đang dùng.
- Centralized logs hoặc log rotation.
- Monitoring uptime và resource.
- Sentry cho lỗi backend/frontend.
- Rate limit cho auth, checkout và webhook.
- Security review cho secret, CORS, upload, permission.
- Quy trình rollback.

Kết quả hoàn thành:

- Có thể deploy production từ CI/CD hoặc script chuẩn.
- Có backup và restore drill tối thiểu.
- Có cảnh báo khi service lỗi hoặc VPS thiếu tài nguyên.

## Phase 5: Scale And Optimization

Mục tiêu: tối ưu khi có dữ liệu sử dụng thật.

Việc cần làm:

- Cache read-heavy endpoints bằng Redis.
- Background jobs với Asynq cho email, notification, sync, cleanup.
- Thêm search engine nếu PostgreSQL search không đủ.
- Tối ưu query bằng index và profiling.
- Thêm read model cho dashboard nếu cần.
- Tách database vật lý, queue hoặc deployment unit cho service có bottleneck rõ về team, traffic hoặc vận hành.

Kết quả hoàn thành:

- Hệ thống xử lý tốt hơn dưới tải thật.
- Các quyết định tách hạ tầng sâu hơn dựa trên metric, không dựa trên dự đoán mơ hồ.

## Thứ Tự Ưu Tiên

1. End-to-end buyer purchase flow.
2. Vendor product/order workflow.
3. Admin moderation workflow.
4. Payment correctness and webhook safety.
5. Production observability and backup.
6. Optimization and scale.
