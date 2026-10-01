# Frontend upgrade — rollout, kiểm thử và rollback

Xem [chi tiết module](../docs/module-details/12-frontend.md).

## Cấu hình

Không có biến mới. `NEXT_PUBLIC_API_BASE_URL` (build arg) vẫn là địa chỉ gateway. Frontend và gateway phải cùng site để cookie phiên `shopee_refresh` (HttpOnly, SameSite=Strict) được gửi; nên đặt cùng origin và reverse proxy `/api`.

## Thay đổi hành vi

- **Đặt hàng:** khóa idempotency của mỗi lần đặt hàng được giữ theo tài khoản (24 giờ, chỉ là khóa ngẫu nhiên và dữ liệu đầu vào, không có token/PII) nên bấm lại, tải lại trang, mất mạng hay mở tab khác vẫn chỉ ra một đơn. Mất phản hồi → báo "chưa rõ đơn đã tạo chưa", bấm lại an toàn; giỏ đã trống thì chỉ tới Đơn hàng của tôi. Tổng tiền đổi → hiện "đổi từ X thành Y", phải bấm xác nhận lại. Bỏ ô voucher chưa có backend.
- **Quay về từ cổng thanh toán:** `/orders/payment-return` và `/orders/payment-cancel` không còn chỉ chuyển hướng: chỉ đọc đơn từ API, hỏi lại 1–2–3–5–8 giây rồi mỗi 10 giây (tối đa 2 phút; trang hủy 30 giây). Hiện: đang xác nhận / đã thanh toán / chưa thanh toán (có nút thanh toán lại, hủy đơn qua API Order có xác nhận) / đã hủy / **chưa xác định** (dặn không thanh toán lại). Query string của cổng (`status=PAID`, `cancel=true`) bị bỏ qua; `order_id` phải là UUID, đơn của người khác không hiện gì.
- Trang đơn hàng tự cập nhật khi đơn đang giữ hàng (`preparing`).
- **Lỗi:** mọi request có timeout (30 giây, upload 120 giây). Mất phản hồi, 429, 5xx luôn hiện thông báo tiếng Việt cố định kèm mã tra cứu (request id), không bao giờ hiện chi tiết lỗi máy chủ.
- **Nội dung:** mô tả sản phẩm bỏ form/input/iframe/style/svg, link chỉ http(s) và mở tab mới với `rel="noopener noreferrer nofollow ugc"`, ảnh chỉ http(s), lazy, không gửi referrer.
- **Header bảo mật:** `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy`; bỏ `X-Powered-By`.
- Checkout trên điện thoại không còn tràn ngang (ô chọn địa chỉ dài).

## Triển khai

1. Build: `bash deploy/build-images.sh frontend`.
2. Deploy `frontend`. Không cần migration; tương thích API hiện tại.
3. Smoke:
   - Đặt một đơn thử, tắt mạng ngay sau khi bấm Đặt hàng, bật lại và bấm lần nữa → chỉ có một đơn.
   - Thanh toán qua cổng → về trang "Đang xác nhận…" → "Thanh toán thành công" khi webhook tới. Mở `/orders/payment-return?order_id=<đơn chưa trả>&status=PAID` → không hiện thành công.
   - Hủy trên cổng → "Bạn chưa hoàn tất thanh toán" → Hủy đơn → đơn chuyển "Đã hủy".
   - Đăng xuất, đăng nhập tài khoản khác → không thấy đơn của tài khoản trước.
   - `curl -I https://<domain>/` có `X-Frame-Options: DENY`.

## Kiểm thử E2E

`npm run test:e2e` (trong `frontend/`) build app trỏ tới gateway giả (`e2e/mock-api.ts`) và chạy trên cổng 3100, desktop và mobile:

| Spec | Nội dung |
|---|---|
| `checkout` | Mất phản hồi rồi tải lại → cùng khóa, một đơn; giỏ trống chỉ tới đơn; double-click một đơn; tổng đổi phải xác nhận lại |
| `payment` | Query string không làm đơn "đã thanh toán"; quá 2 phút → chưa xác định; trang hủy → hủy qua API; đơn người khác / id lỗi không hiện gì |
| `session` | Đổi tài khoản không thấy đơn cũ; không token trong storage; đơn người khác; buyer vào admin bị chuyển đi, không gọi API admin |
| `content-safety` | Script/onerror/form/`javascript:` trong mô tả và review không chạy; link an toàn; header bảo mật |
| `mobile` | Checkout, đơn hàng, trang hủy không tràn ngang; nút đặt hàng dùng được |

Local không tải được Chromium thì dùng Chrome đã cài: `E2E_CHANNEL=chrome npm run test:e2e`. CI chạy job `frontend-e2e` (tải Chromium), lưu report khi lỗi.

E2E dùng gateway giả: kiểm tra hành vi frontend. Cookie/CSRF/revocation thật đã có test ở Identity; chạy smoke ở trên với stack thật trước khi mở bán.

## Rollback

Image frontend cũ đọc được API hiện tại. Rollback mất: khóa đặt hàng qua reload (vẫn giữ trong phiên trang), trang trạng thái thanh toán (quay về chuyển hướng thẳng tới trang đơn — vẫn đọc từ API), timeout request, header bảo mật. Không cần thao tác dữ liệu; khóa đã lưu trong trình duyệt tự hết hạn sau 24 giờ.
