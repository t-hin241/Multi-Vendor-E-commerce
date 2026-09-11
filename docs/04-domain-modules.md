# Domain Modules

Các domain dưới đây được xem là bounded context cho kiến trúc microservices. Mỗi domain nên triển khai thành một service riêng hoặc một service nhỏ cùng nhóm trong giai đoạn MVP, nhưng vẫn phải giữ ownership dữ liệu, API contract và event contract rõ ràng.

## Identity

Trách nhiệm:

- Quản lý user account.
- Đăng ký, đăng nhập, refresh token, logout.
- Quên mật khẩu và đổi mật khẩu.
- Role buyer, vendor, admin.
- Session/token lifecycle.

Entity chính:

- User.
- UserCredential.
- Role.
- RefreshToken hoặc Session.
- PasswordResetToken.

Rules quan trọng:

- Email là định danh đăng nhập duy nhất.
- Password phải hash bằng thuật toán phù hợp như bcrypt hoặc argon2.
- Token có thời hạn và có thể bị revoke.
- Role check phải thực hiện ở cả middleware và use case nhạy cảm.

## Vendor

Trách nhiệm:

- Vendor profile.
- Onboarding.
- Duyệt hoặc từ chối vendor.
- Trạng thái hoạt động của vendor.

Entity chính:

- Vendor.
- VendorApplication.
- VendorStatus.

Rules quan trọng:

- Vendor chưa approved không được publish sản phẩm.
- Một user có thể gắn với một vendor trong MVP.
- Admin action nên có audit trail.

## Catalog

Trách nhiệm:

- Product.
- Category.
- Product media.
- Product visibility.
- Storefront product listing.

Entity chính:

- Product.
- ProductVariant nếu cần size/màu đơn giản.
- Category.
- ProductImage.
- ProductStatus.

Rules quan trọng:

- Sản phẩm chỉ hiển thị khi vendor approved, product approved và product active.
- Giá bán phải là số dương.
- Product slug hoặc SKU nên unique trong phạm vi phù hợp.
- Ảnh upload phải được validate.

## Inventory

Trách nhiệm:

- Quản lý tồn kho.
- Reserve stock khi checkout.
- Release stock khi payment fail hoặc order timeout.
- Ghi nhận stock movement.

Entity chính:

- InventoryItem.
- StockReservation.
- StockMovement.

Rules quan trọng:

- Không bán quá tồn kho.
- Reserve phải có thời hạn.
- Stock movement cần trace được lý do thay đổi.

## Cart

Trách nhiệm:

- Quản lý giỏ hàng buyer.
- Thêm, sửa, xóa item.
- Tính subtotal tạm thời.
- Chuẩn bị checkout.

Entity chính:

- Cart.
- CartItem.

Rules quan trọng:

- Cart item phải tham chiếu sản phẩm đang bán được.
- Số lượng phải lớn hơn 0.
- Cart có thể chứa sản phẩm từ nhiều vendor, nhưng khi checkout phải tách order theo vendor.

## Order

Trách nhiệm:

- Tạo và quản lý order.
- Tách vendor sub-order.
- Quản lý trạng thái order.
- Lưu pricing snapshot.

Entity chính:

- Order.
- OrderItem.
- VendorOrder.
- OrderStatus.
- OrderAddress.

Rules quan trọng:

- Giá trong order item là snapshot tại thời điểm checkout.
- Order status transition phải hợp lệ.
- Buyer chỉ xem order của mình.
- Vendor chỉ xem vendor order thuộc vendor của mình.
- Admin xem được toàn bộ.

Trạng thái đề xuất:

- pending_payment.
- paid.
- processing.
- shipped.
- completed.
- cancelled.
- refunded.

## Payment

Trách nhiệm:

- Tạo payment intent.
- Lưu transaction.
- Xác thực webhook.
- Đồng bộ payment status với order.

Entity chính:

- Payment.
- PaymentTransaction.
- PaymentProviderEvent.
- PaymentStatus.

Rules quan trọng:

- Webhook phải verify chữ ký nếu provider hỗ trợ.
- Provider event id phải unique để chống xử lý trùng.
- Payment không tự ý sửa order nếu không qua use case hoặc domain contract.

Trạng thái đề xuất:

- pending.
- authorized.
- captured.
- failed.
- refunded.

## Shipment

Trách nhiệm:

- Theo dõi trạng thái giao hàng tối thiểu.
- Lưu tracking code.
- Cho vendor cập nhật fulfillment state.

Entity chính:

- Shipment.
- ShipmentStatus.
- TrackingInfo.

Rules quan trọng:

- Chỉ order paid mới được xử lý giao hàng.
- Vendor chỉ cập nhật shipment của vendor order thuộc mình.
- Delivered hoặc completed nên có timestamp rõ.

Trạng thái đề xuất:

- pending.
- ready_to_ship.
- shipped.
- delivered.
- failed.

## Admin

Trách nhiệm:

- Duyệt vendor.
- Duyệt sản phẩm.
- Xem và can thiệp order.
- Quản lý commission rule cơ bản.
- Theo dõi operational dashboard.

Entity chính:

- AdminAuditLog.
- ModerationDecision.
- CommissionRule.

Rules quan trọng:

- Mọi hành động nhạy cảm của admin cần audit log.
- Admin không nên bypass domain rules trừ khi có flow can thiệp rõ ràng.
- Dashboard query có thể dùng read model sau khi dữ liệu lớn.

## Notification

Trách nhiệm:

- Gửi email hoặc notification cho sự kiện quan trọng.
- Quản lý template.
- Retry khi gửi lỗi.

Entity chính:

- Notification.
- NotificationTemplate.
- DeliveryAttempt.

Events ban đầu:

- User registered.
- Vendor approved/rejected.
- Product approved/rejected.
- Order created.
- Payment succeeded/failed.
- Order shipped.

Rules quan trọng:

- Gửi notification qua background job.
- Retry có giới hạn.
- Không để lỗi gửi email làm hỏng transaction chính.
