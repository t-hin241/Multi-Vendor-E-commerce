# Architecture

## Định Hướng

Hệ thống sử dụng kiến trúc microservices ngay từ đầu, nhưng triển khai theo hướng tối giản để vẫn phù hợp với MVP. Mỗi service đại diện cho một bounded context rõ ràng, sở hữu business rules và dữ liệu chính của mình, giao tiếp với service khác qua API contract hoặc event contract được định nghĩa rõ.

Mục tiêu không phải là tạo nhiều service cho phức tạp, mà là tách các năng lực cốt lõi của marketplace thành những service nhỏ, deploy được độc lập, quan sát được, test được và có thể scale riêng khi có nhu cầu thực tế.

## Kiến Trúc Service

Mỗi service nên có các lớp nhẹ:

- Handler/transport: nhận HTTP/gRPC request, parse input, gọi use case.
- Use case/service: điều phối business flow trong phạm vi service.
- Domain: entity, value object, domain rules.
- Repository: đọc/ghi database do service sở hữu.
- Integration adapter: kết nối provider hoặc service bên ngoài.

Luồng phụ thuộc trong một service:

```text
transport -> use case -> domain/repository/adapter
```

Domain không nên phụ thuộc HTTP framework, database library, message broker hoặc SDK provider.

## Service Boundaries

Các service khởi đầu:

- Identity Service: user, auth, role, session, password reset.
- Vendor Service: vendor profile, onboarding, approval status.
- Catalog Service: product, category, product media, product status.
- Inventory Service: stock, reservation, stock movement.
- Cart Service: cart item, pricing snapshot trước checkout.
- Order Service: order, order item, order status, vendor sub-order.
- Payment Service: payment intent, payment transaction, webhook event.
- Shipment Service: shipment state, tracking info tối thiểu.
- Admin/BFF Service: moderation workflow và operational views.
- Notification Service: email, in-app notification, event templates.

Nguyên tắc boundary:

- Mỗi service sở hữu database schema hoặc database riêng theo phạm vi của mình.
- Service không đọc/ghi trực tiếp bảng của service khác.
- Service giao tiếp đồng bộ qua API khi cần kết quả ngay.
- Service giao tiếp bất đồng bộ qua event khi xử lý hậu kỳ hoặc đồng bộ trạng thái.
- Catalog không tự xử lý payment.
- Payment không tự quyết định order business rules.
- Order gọi Inventory qua contract để reserve hoặc release stock.
- Vendor sở hữu trạng thái vendor; Catalog kiểm tra quyền bán qua contract hoặc read model được đồng bộ.
- Notification nhận event và gửi message, không thay đổi trạng thái domain chính.

## Data Flow: Buyer Checkout

Luồng chính:

1. Buyer thêm sản phẩm vào cart qua Cart Service.
2. Cart Service lấy thông tin giá và tình trạng bán từ Catalog Service.
3. Buyer checkout qua Checkout/Order API.
4. Order Service tạo pending order và vendor sub-orders.
5. Order Service yêu cầu Inventory Service reserve stock.
6. Payment Service tạo payment intent qua provider adapter.
7. Buyer thanh toán trên provider.
8. Provider gửi webhook tới Payment Service.
9. Payment Service xác thực webhook, lưu event id, cập nhật payment status.
10. Payment Service phát event `PaymentSucceeded` hoặc `PaymentFailed`.
11. Order Service nhận event payment và chuyển order sang paid hoặc failed nếu hợp lệ.
12. Notification Service nhận event và gửi email cho buyer/vendor.

Yêu cầu quan trọng:

- Checkout phải dùng pricing snapshot để tránh thay đổi giá sau khi tạo đơn.
- Webhook phải idempotent.
- Stock reservation phải có timeout hoặc cleanup nếu đơn không thanh toán.
- Event consumer phải idempotent vì message có thể được gửi lặp.
- Order và Payment phải kiểm tra amount, currency và order reference trước khi đổi trạng thái.

## Data Flow: Vendor Product Publishing

Luồng chính:

1. Vendor tạo hoặc cập nhật sản phẩm qua Catalog Service.
2. Catalog Service kiểm tra vendor có được phép bán qua Vendor Service hoặc vendor read model.
3. Catalog Service lưu sản phẩm ở trạng thái draft hoặc pending_review.
4. Admin duyệt hoặc từ chối qua Admin/BFF Service.
5. Admin/BFF Service gọi Catalog Service để cập nhật moderation decision.
6. Sản phẩm approved và active mới xuất hiện trên storefront.
7. Catalog Service phát event `ProductApproved` hoặc `ProductRejected`.

Yêu cầu quan trọng:

- Buyer không thấy sản phẩm chưa được duyệt.
- Vendor chỉ sửa được sản phẩm thuộc vendor của mình.
- Admin có audit trail cho hành động duyệt/từ chối.
- Read model phục vụ storefront phải nhất quán cuối cùng với Catalog Service.

## Data Flow: Order Fulfillment

Luồng chính:

1. Order paid xuất hiện trong vendor dashboard qua Order Service hoặc Admin/BFF read model.
2. Vendor xác nhận xử lý đơn.
3. Shipment Service tạo hoặc cập nhật shipment cho vendor order.
4. Shipment Service phát event `OrderShipped` hoặc `OrderDelivered`.
5. Order Service nhận event fulfillment và cập nhật trạng thái order.
6. Khi delivered hoặc completed, Order Service phát event để ghi nhận doanh thu và commission.

Yêu cầu quan trọng:

- Vendor chỉ nhìn thấy sub-order thuộc vendor của mình.
- Admin có quyền xem toàn bộ order thông qua Admin/BFF Service.
- Order status transition phải được validate trong Order Service.
- Cross-service update phải idempotent và có retry policy rõ.

## Database Strategy

Giai đoạn đầu vẫn có thể dùng một PostgreSQL cluster để giảm chi phí vận hành, nhưng mỗi service phải sở hữu schema riêng hoặc database riêng. Quy tắc quan trọng là ownership theo service, không phải số lượng server database.

Khuyến nghị:

- Mỗi service có migration riêng.
- Không tạo foreign key trực tiếp qua schema của service khác.
- Dữ liệu cần đọc chéo nên đi qua API hoặc read model được đồng bộ bằng event.
- Index cho truy vấn thường dùng: product listing, order by user, order by vendor, webhook event id.
- Khi tải tăng, tách database vật lý theo service có bottleneck trước.

## API Strategy

API public ban đầu có thể là REST qua API Gateway hoặc Backend-for-Frontend.

Nhóm endpoint public:

- `/api/auth/*`
- `/api/buyer/*`
- `/api/vendor/*`
- `/api/admin/*`
- `/api/webhooks/*`

Nguyên tắc:

- API Gateway xử lý routing, TLS termination, rate limit cơ bản và request id.
- Service nội bộ vẫn phải tự kiểm tra quyền, không chỉ dựa vào gateway.
- Endpoint public phải validate input chặt.
- Response error có format thống nhất.
- Auth context truyền giữa services bằng token hoặc signed internal context.
- Admin/vendor endpoint luôn kiểm tra quyền ở use case.

## Event Strategy

Microservices cần event contract rõ để giảm coupling giữa services. Giai đoạn MVP có thể dùng RabbitMQ, NATS hoặc Redis Streams; không cần Kafka nếu chưa có nhu cầu throughput lớn.

Events nên có:

- `VendorApproved`
- `VendorRejected`
- `ProductApproved`
- `ProductRejected`
- `OrderCreated`
- `InventoryReserved`
- `InventoryReservationFailed`
- `PaymentSucceeded`
- `PaymentFailed`
- `OrderPaid`
- `OrderShipped`
- `OrderCompleted`

Nguyên tắc:

- Event payload phải có event id, event type, occurred_at, aggregate id và correlation id.
- Consumer phải idempotent.
- Event versioning phải được tính từ đầu.
- Không gửi secret trong event payload.
- Dùng outbox pattern cho event phát từ transaction quan trọng như order, payment, inventory.

## Service Operations

Mỗi service cần có:

- Dockerfile riêng hoặc build target riêng.
- Health check endpoint.
- Structured logging.
- Metrics cơ bản.
- Config riêng qua environment variables.
- Migration riêng nếu service sở hữu database schema.
- CI test riêng cho service đó.

Production MVP có thể chạy nhiều service bằng Docker Compose trên VPS. Khi traffic và đội vận hành lớn hơn, mới cân nhắc chuyển sang Kubernetes hoặc nền tảng orchestration managed.
