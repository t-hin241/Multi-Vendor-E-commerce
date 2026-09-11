# Vision: Multi Vendor E-commerce

## Mục Tiêu Sản Phẩm

Xây dựng một nền tảng thương mại điện tử nhiều nhà bán hàng, nơi buyer có thể tìm kiếm sản phẩm, đặt hàng và thanh toán; vendor có thể đăng bán, quản lý đơn hàng; admin có thể kiểm duyệt, vận hành và theo dõi toàn hệ thống.

Phiên bản đầu tiên tập trung vào MVP có thể bán hàng thật, triển khai được lên production bằng Docker trên VPS, có kiến trúc đủ rõ để mở rộng mà không làm chậm tốc độ phát triển ban đầu.

## Đối Tượng Sử Dụng

- Buyer: người mua hàng, cần trải nghiệm tìm kiếm, giỏ hàng, checkout và theo dõi đơn đơn giản.
- Vendor: nhà bán hàng, cần đăng sản phẩm, quản lý tồn kho, xử lý đơn và xem doanh thu cơ bản.
- Admin: đội vận hành marketplace, cần duyệt vendor, duyệt sản phẩm, xử lý rủi ro, cấu hình commission và theo dõi trạng thái hệ thống.
- Developer/operator: đội xây dựng và vận hành, cần codebase rõ module, dễ test, dễ deploy, dễ debug.

## Giá Trị Cốt Lõi

- Marketplace chạy được end-to-end: từ đăng sản phẩm đến thanh toán và tạo đơn.
- Phân quyền rõ giữa buyer, vendor và admin.
- Dễ phát triển tiếp: bắt đầu bằng microservices tối giản, giữ service boundary đủ rõ để mỗi năng lực marketplace có thể deploy và scale độc lập khi cần.
- Vận hành thực dụng: log, backup, monitoring, secret management và rollback được tính từ sớm.

## Phạm Vi MVP

MVP nên bao gồm:

- Đăng ký, đăng nhập, quên mật khẩu cơ bản.
- Vai trò buyer, vendor, admin.
- Vendor onboarding với trạng thái chờ duyệt.
- Quản lý sản phẩm, danh mục, ảnh sản phẩm và tồn kho đơn giản.
- Storefront cho buyer xem danh sách, xem chi tiết, tìm kiếm cơ bản.
- Giỏ hàng và checkout.
- Tạo đơn hàng, chia đơn theo vendor nếu một cart có nhiều vendor.
- Thanh toán qua adapter, ưu tiên Stripe hoặc PayPal trước.
- Webhook thanh toán để cập nhật trạng thái đơn.
- Vendor dashboard cơ bản để xem và xử lý đơn.
- Admin dashboard cơ bản để duyệt vendor, duyệt sản phẩm và xem đơn hàng.
- Email/notification cơ bản cho các sự kiện quan trọng.

## Ngoài Phạm Vi MVP

Những phần sau nên để sau khi MVP ổn định:

- Recommendation engine.
- Flash sale, voucher phức tạp, loyalty points.
- Dispute center đầy đủ.
- Payout tự động nhiều quốc gia.
- Warehouse management nâng cao.
- Mobile app native.
- Kubernetes hoặc service mesh phức tạp.
- Multi-tenant SaaS/white-label.

## Tiêu Chí Thành Công Cho MVP

- Buyer có thể mua một sản phẩm thật từ vendor đã được duyệt.
- Vendor có thể tạo sản phẩm, nhận đơn, cập nhật trạng thái xử lý.
- Admin có thể kiểm soát vendor, sản phẩm và đơn hàng.
- Hệ thống có migration, seed data, test cơ bản và chạy được bằng Docker Compose.
- Có production checklist và quy trình deploy lên VPS.
- Lỗi quan trọng được log có cấu trúc và có thể tra cứu.

## Nguyên Tắc Sản Phẩm

- Ưu tiên workflow bán hàng thật hơn tính năng trang trí.
- Mọi trạng thái quan trọng phải explicit: vendor status, product status, order status, payment status, shipment status.
- Thiết kế đơn giản trước, nhưng tránh khóa chặt domain vào một payment provider, một shipping provider hoặc một UI duy nhất.
- Không tối ưu hóa quy mô quá sớm; microservices phải bám bounded context rõ và tránh tạo service thừa khi chưa có nhu cầu vận hành thực tế.
