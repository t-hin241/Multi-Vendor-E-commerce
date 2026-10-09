package domain

import (
	"fmt"
	"strings"
)

// Template renders one notification type. Messages state only what the
// producing service already decided (a verified capture, a confirmed
// refund, a vendor decision), never more, and carry no secret or link with
// a token.
type Template struct {
	Version string
	Subject string
	Body    string // %s: reference (short)
}

var templates = map[Type]Template{
	"sla_support":      {"v1", "Hồ sơ hỗ trợ #%s cần được xử lý", "Hồ sơ đến hạn hoặc đã quá hạn. Mở /admin/support/%s để kiểm tra và xử lý theo quyền của bạn."},
	"sla_return":       {"v1", "Yêu cầu trả hàng #%s cần được xử lý", "Yêu cầu đến hạn hoặc đã quá hạn. Mở /admin/returns?return_id=%s để kiểm tra. Thông báo này không phê duyệt hoàn tiền."},
	"sla_refund":       {"v1", "Yêu cầu hoàn tiền #%s cần được xử lý", "Yêu cầu đến hạn hoặc đã quá hạn. Mở /admin/refunds?refund_id=%s để kiểm tra. Thông báo này không xác nhận tiền đã được hoàn."},
	"sla_cancellation": {"v1", "Yêu cầu hủy đơn #%s cần được xử lý", "Yêu cầu hủy sau thanh toán đến hạn hoặc đã quá hạn. Mở /admin/cancellations?request_id=%s để quyết định. Thông báo này không hủy đơn và không hoàn tiền."},
	"sla_interception": {"v1", "Yêu cầu dừng giao #%s cần được xử lý", "Yêu cầu đến hạn hoặc đã quá hạn. Mở /admin/fulfillment?shipment_id=%s để kiểm tra và liên hệ đơn vị vận chuyển."},

	"sla_delivery_exception": {"v1", "Hồ sơ giao hàng thất bại #%s cần được xử lý", "Hồ sơ đến hạn hoặc đã quá hạn. Mở /admin/delivery-exceptions?exception_id=%s để kiểm tra chứng cứ và quyết định giao lại hoặc hoàn tiền. Thông báo này không hoàn tiền và không tạo lần giao mới."},

	TypeOrderPaid: {"v1", "Đơn hàng #%s đã được thanh toán",
		"Chúng tôi đã nhận được thanh toán cho đơn hàng #%s. Người bán sẽ chuẩn bị hàng cho bạn."},
	TypeOrderShipped: {"v1", "Đơn hàng #%s đã được giao cho đơn vị vận chuyển",
		"Một kiện hàng của đơn #%s đã được giao cho đơn vị vận chuyển. Bạn có thể xem mã vận đơn trong trang đơn hàng."},
	TypeOrderCompleted: {"v1", "Đơn hàng #%s đã giao thành công",
		"Đơn hàng #%s đã được giao thành công. Cảm ơn bạn đã mua sắm."},
	TypeOrderCancelled: {"v1", "Đơn hàng #%s đã bị hủy",
		"Đơn hàng #%s đã bị hủy. Nếu bạn đã thanh toán, khoản hoàn tiền sẽ được thông báo riêng khi được xác nhận."},
	TypeOrderRefunded: {"v1", "Hoàn tiền cho đơn hàng #%s đã được xác nhận",
		"Khoản hoàn tiền cho đơn hàng #%s đã được xác nhận. Thời gian tiền về tài khoản tùy thuộc ngân hàng hoặc ví của bạn."},
	TypeVendorApproved: {"v1", "Cửa hàng của bạn đã được duyệt",
		"Cửa hàng (mã %s) đã được duyệt. Bạn có thể đăng sản phẩm để bán."},
	TypeVendorRejected: {"v1", "Đăng ký cửa hàng chưa được duyệt",
		"Đăng ký cửa hàng (mã %s) chưa được duyệt. Xem lý do trong trang quản lý cửa hàng và gửi lại sau khi chỉnh sửa."},
	TypeSupportCaseOpened: {"v1", "Đã nhận yêu cầu hỗ trợ cho đơn hàng #%s",
		"Chúng tôi đã nhận yêu cầu hỗ trợ của bạn cho đơn hàng #%s. Bạn có thể theo dõi phản hồi trong trang chi tiết đơn hàng."},
	TypeSupportCaseResolved: {"v1", "Yêu cầu hỗ trợ cho đơn hàng #%s đã có kết luận",
		"Yêu cầu hỗ trợ cho đơn hàng #%s đã có kết luận. Xem chi tiết trong trang đơn hàng; nếu chưa đồng ý, bạn có thể mở lại trong 7 ngày."},
	TypeCancellationRequested: {"v1", "Đã nhận yêu cầu hủy cho đơn hàng #%s",
		"Chúng tôi đã nhận yêu cầu hủy một phần đơn hàng #%s và tạm dừng giao phần này trong khi xem xét. Đơn chưa bị hủy cho đến khi có thông báo tiếp theo."},
	TypeCancellationApproved: {"v1", "Yêu cầu hủy cho đơn hàng #%s đã được chấp nhận",
		"Phần đơn hàng #%s đã được dừng giao. Khoản hoàn tiền sẽ được thông báo riêng khi được xác nhận; chưa có tiền nào được hoàn ở bước này."},
	TypeCancellationRejected: {"v1", "Yêu cầu hủy cho đơn hàng #%s chưa được chấp nhận",
		"Yêu cầu hủy cho đơn hàng #%s chưa được chấp nhận. Xem lý do trong trang chi tiết đơn hàng; đơn tiếp tục được giao."},
	TypeDeliveryExceptionOpened: {"v1", "Kiện hàng của đơn #%s chưa giao được",
		"Một kiện hàng của đơn #%s chưa giao được cho bạn. Sàn đang xử lý; bạn sẽ được hỏi trước khi giao lại, và khoản hoàn tiền (nếu có) chỉ được báo khi đã xác nhận."},
	TypeRedeliveryOffered: {"v1", "Xác nhận giao lại kiện hàng của đơn #%s",
		"Sàn đề nghị giao lại một kiện hàng của đơn #%s. Mở trang chi tiết đơn hàng để xác nhận địa chỉ nhận; bạn không phải trả thêm phí."},
	TypeDeliveryExceptionResolved: {"v1", "Sự cố giao hàng của đơn #%s đã được xử lý",
		"Sự cố giao hàng của đơn #%s đã có kết quả. Xem chi tiết trong trang đơn hàng; khoản hoàn tiền (nếu có) được báo riêng khi đã xác nhận."},
	TypeReturnShippingInstructions: {"v1", "Hướng dẫn gửi trả hàng cho đơn #%s",
		"Yêu cầu trả hàng của đơn #%s đã được chấp nhận. Mở trang Trả hàng để xem địa chỉ nhận, mã trả hàng và hạn gửi; sau khi gửi, nhập mã vận đơn. Tiền hoàn chỉ được báo khi người bán đã nhận hàng và khoản hoàn được xác nhận."},
	TypeMarketplacePolicyUpdated: {"v1", "Chính sách sàn có phiên bản mới (cửa hàng %s)",
		"Sàn đã công bố phiên bản chính sách mới áp dụng cho cửa hàng (mã %s). Đơn đã đặt giữ chính sách cũ; xem nội dung tại trang Chính sách."},
	TypeShopPolicyApproved: {"v1", "Chính sách cửa hàng %s đã được duyệt",
		"Chính sách bổ sung của cửa hàng (mã %s) đã được duyệt và hiển thị cho người mua."},
	TypeShopPolicyRejected: {"v1", "Chính sách cửa hàng %s chưa được duyệt",
		"Chính sách bổ sung của cửa hàng (mã %s) chưa được duyệt. Xem lý do trong trang quản lý cửa hàng và gửi lại sau khi chỉnh sửa."},
}

// Render builds the subject and plain-text body. name comes from Identity
// and is used only in the greeting.
func Render(n *Notification, name string) (subject, body string, err error) {
	t, ok := templates[n.Type]
	if !ok || t.Version != n.TemplateVersion {
		return "", "", fmt.Errorf("no template %s/%s", n.Type, n.TemplateVersion)
	}
	ref := n.ReferenceID
	if len(ref) > 8 {
		ref = ref[:8]
	}
	greeting := "Xin chào,"
	if name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)); name != "" {
		greeting = "Xin chào " + name + ","
	}
	bodyRef := ref
	if strings.HasPrefix(string(n.Type), "sla_") {
		bodyRef = n.ReferenceID
	}
	return fmt.Sprintf(t.Subject, ref), greeting + "\n\n" + fmt.Sprintf(t.Body, bodyRef) + "\n\nĐây là email tự động, vui lòng không trả lời.\n", nil
}

// KnownType reports whether t has a template.
func KnownType(t Type) bool {
	_, ok := templates[t]
	return ok
}
