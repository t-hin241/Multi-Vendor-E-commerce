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
	return fmt.Sprintf(t.Subject, ref), greeting + "\n\n" + fmt.Sprintf(t.Body, ref) + "\n\nĐây là email tự động, vui lòng không trả lời.\n", nil
}

// KnownType reports whether t has a template.
func KnownType(t Type) bool {
	_, ok := templates[t]
	return ok
}
