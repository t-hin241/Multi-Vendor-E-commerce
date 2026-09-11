# Tech Stack

## Định Hướng Chung

Tech stack ưu tiên tính thực dụng, dễ tuyển người, dễ deploy và phù hợp với Golang. Kiến trúc mặc định là microservices tối giản: mỗi service sở hữu bounded context rõ, dữ liệu của mình và contract giao tiếp qua API hoặc event.

## Backend

Đề xuất chính:

- Language: Go.
- HTTP framework: Gin hoặc Echo.
- API Gateway/BFF: Caddy/Nginx routing ban đầu hoặc một Go gateway riêng nếu cần auth/routing phức tạp.
- Database access: sqlc nếu muốn type-safe SQL; GORM nếu muốn tốc độ phát triển nhanh hơn.
- Migration: Goose, Atlas hoặc golang-migrate.
- Validation: go-playground/validator.
- Config: environment variables, có thể dùng Viper nếu cấu hình phức tạp.
- Logging: zap hoặc zerolog.
- Auth: JWT access token, refresh token lưu server-side hoặc token rotation.

Khuyến nghị mặc định:

- Dùng Gin nếu team muốn ecosystem lớn và nhiều ví dụ.
- Dùng sqlc nếu ưu tiên kiểm soát query, performance và type safety.
- Dùng GORM nếu đội nhỏ cần build nhanh MVP và chấp nhận kiểm soát SQL ít hơn.

## Database

Đề xuất chính:

- PostgreSQL làm database chính, tách schema hoặc database theo service.
- Redis cho cache, rate limit, session/token store và job backend.
- Message broker như NATS hoặc RabbitMQ cho event giữa services.

Lý do:

- PostgreSQL đủ mạnh cho transactional commerce data và có thể bắt đầu bằng một cluster để giảm chi phí vận hành.
- Redis giúp giảm tải ở những điểm nóng như catalog, session, rate limit và background jobs.
- Message broker giúp giảm coupling giữa Order, Payment, Inventory, Notification và các service xử lý hậu kỳ.

## Queue And Background Jobs

Đề xuất:

- Asynq trên Redis cho background jobs trong từng service.
- NATS hoặc RabbitMQ cho domain events giữa services.

Use cases:

- Gửi email.
- Xử lý notification.
- Retry payment/order side effects.
- Cleanup expired carts, unpaid orders, temporary uploads.
- Đồng bộ dữ liệu với provider bên ngoài.
- Phát và tiêu thụ events như `OrderCreated`, `PaymentSucceeded`, `InventoryReserved`.

Nguyên tắc:

- API request không nên chờ những việc không cần đồng bộ.
- Job phải idempotent để retry an toàn.
- Event consumer phải idempotent vì message có thể được gửi lặp.

## Frontend

Đề xuất:

- Next.js cho storefront, vendor portal và admin portal.
- TypeScript.
- React Query hoặc TanStack Query cho server state.
- Tailwind CSS hoặc một design system nhất quán.

Tổ chức ban đầu:

- Có thể dùng một Next.js app với route groups cho buyer, vendor và admin.
- Khi sản phẩm lớn hơn, tách admin/vendor portal thành app riêng nếu cần release độc lập.

## Storage

Đề xuất:

- S3-compatible object storage cho ảnh sản phẩm và file export.
- Local filesystem chỉ dùng cho development.

Provider phù hợp:

- AWS S3.
- Cloudflare R2.
- DigitalOcean Spaces.
- MinIO cho local development.

Nguyên tắc:

- Backend tạo signed URL hoặc proxy upload có kiểm soát.
- Validate content type, dung lượng và kích thước ảnh.
- Không lưu file upload lâu dài trên container filesystem.

## Payment

Đề xuất:

- Stripe hoặc PayPal cho bản đầu.
- Thiết kế payment adapter để thêm cổng nội địa sau.

Nguyên tắc:

- Không để domain order phụ thuộc trực tiếp SDK của provider.
- Webhook phải idempotent.
- Lưu provider event id để chống xử lý trùng.
- Trạng thái payment và order nên tách riêng nhưng đồng bộ bằng domain rules rõ ràng.

## DevOps

Local development:

- Docker Compose cho các services, PostgreSQL, Redis, message broker, MinIO và app dependencies.
- Makefile hoặc task runner cho lệnh phổ biến.

Production MVP:

- Dockerfile production cho backend và frontend.
- Docker Compose production trên VPS cho nhiều services.
- Caddy hoặc Nginx làm reverse proxy.
- GitHub Actions để test, build image và deploy.

Secrets:

- Không commit `.env`.
- Dùng GitHub Actions secrets cho CI/CD.
- Trên VPS dùng `.env.production` được quản lý thủ công hoặc secret manager nếu có.

## Observability

Đề xuất:

- Structured logs dạng JSON.
- Request ID cho từng request.
- Sentry cho error tracking.
- Prometheus/Grafana hoặc Grafana Cloud cho metrics.
- Uptime monitoring bên ngoài.

Metrics quan trọng:

- Request latency theo endpoint.
- Error rate.
- Payment webhook success/failure.
- Order creation rate.
- Queue length và job failure.
- Cross-service event lag và consumer failure.
- Database connection pool usage.
- CPU, memory, disk usage trên VPS.

## Testing

Đề xuất:

- Go unit tests cho domain logic.
- Integration tests với PostgreSQL/Redis test containers hoặc Docker Compose test profile.
- API tests cho luồng buyer/vendor/admin.
- Frontend component và E2E tests cho checkout critical path.

Lệnh kỳ vọng:

- `go test ./...`
- `go test -race ./...` cho CI hoặc nightly.
- `npm test` hoặc `pnpm test` cho frontend.
- Smoke test sau deploy.

## Công Cụ Bổ Trợ

- OpenAPI để mô tả API contract.
- Swagger UI hoặc Scalar cho API docs nội bộ.
- golangci-lint cho linting.
- Air cho hot reload local.
- Dependabot hoặc Renovate cho dependency updates.
