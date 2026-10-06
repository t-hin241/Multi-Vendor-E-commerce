# Review upgrade — rollout, vận hành và rollback

Xem [chi tiết module](../docs/module-details/11-review.md). Quyết định D05: Review **bật** trong release đầu, với các lỗ hổng ownership/upload/PII đã sửa trước khi mở route.

## Cấu hình

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `REVIEW_SHOW_UNVERIFIED` | `false` | `true`: storefront hiện cả review không phải "đã mua hàng" (dữ liệu seed demo). Chỉ local/staging; service từ chối khởi động khi `ENV=production` |
| `IDENTITY_SERVICE_KEY` | dùng chung | Gọi Order (eligibility), Vendor (chủ shop), Identity (tên, xác minh admin) |
| `REDIS_URL` | riêng | Bộ đếm rate limit (`review:rate:*`, mất được) trên Redis cache, user ACL `review`; `REVIEW_REDIS_PASSWORD` |
| `OBJECT_STORAGE_*` | có sẵn | Bucket `review-images` |

Không có secret mới. Review không còn kết nối NATS (không dùng).

## Thay đổi hành vi

- **Sửa lỗi chặn tính năng:** trước đây Review không đọc được dữ liệu eligibility của Order (sai tên field JSON), nên không ai tạo được review; báo cáo, lý do và eligibility trả frontend cũng sai tên field. Đã sửa, có test hồi quy.
- Tạo review: chỉ item Order xác nhận thuộc vendor order `completed` của chính buyer; mỗi item một review (409 khi lặp, kể cả gửi đồng thời). Order không trả lời → 503, không quyết định gì. Review giữ nguyên nếu đơn sau đó trả hàng/hoàn tiền.
- Công khai: tên tác giả được che (`N***n`), chụp khi tạo; không gọi Identity khi xem danh sách. Không trả buyer id, order item, ghi chú kiểm duyệt. Nhãn "Đã mua hàng" chỉ cho review được xác minh.
- Nội dung: bỏ ký tự điều khiển và ký tự đảo chiều văn bản; hiển thị dạng văn bản thuần.
- Ảnh: kiểm tra nội dung thật, kích thước, độ phân giải; lưu bản mã hóa lại (xoay đúng chiều, bỏ EXIF/GPS). Tối đa 5 ảnh, kể cả upload đồng thời. Upload lỗi không để lại object: có bản ghi upload và worker dọn lại.
- Shop: chỉ chủ shop trả lời/báo cáo review của shop mình, không với review đã ẩn; có audit.
- Admin: giải quyết báo cáo (ẩn cần lý do), ẩn trực tiếp (lý do + ghi chú), **hiển thị lại** (ghi chú), mọi thay đổi lý do đều được audit và tra cứu được ở `/admin/audit`.
- Rate limit theo người dùng mỗi giờ: tạo review 20, ảnh 40, trả lời 60, báo cáo 30 (Redis lỗi thì cho qua và ghi log).
- API mới: `POST /api/reviews/admin/:id/hide`, `POST /api/reviews/admin/:id/restore`, `GET /api/reviews/admin/operations`.

## Thứ tự triển khai

1. Backup `review_db`.
2. Build: `bash deploy/build-images.sh review admin frontend`.
3. Migration: Review `000003_review_hardening`. Review cũ (đều từ seed) có `verified_purchase = false`; tên tác giả được điền dần bởi backfill.
4. Deploy `review`, rồi `admin`, rồi `frontend` (UI cũ đọc được API mới; UI mới cần API mới).
5. Smoke trên staging:
   - Buyer có đơn `completed` → viết review kèm 1 ảnh → trang sản phẩm thấy tên đã che, nhãn "Đã mua hàng", ảnh không còn EXIF.
   - Buyer chưa mua → không thấy form, gọi API trả 403.
   - Shop khác trả lời/báo cáo → 403; chủ shop báo cáo → `/admin/reviews` thấy báo cáo; ẩn với lý do → biến mất khỏi trang sản phẩm và tổng điểm; hiển thị lại với ghi chú.
   - `/admin/audit` nguồn review thấy các thao tác trên.

## Theo dõi

`GET /api/reviews/admin/operations` (dashboard `/admin`: "Review reports waiting for a decision", "Review photos left behind by failed uploads"):

| Chỉ số | Ý nghĩa | Ngưỡng gợi ý |
|---|---|---|
| `open_reports`, `oldest_open_report_hours` | Backlog kiểm duyệt | báo cáo cũ hơn 48 giờ |
| `image_cleanup_pending` | Upload lỗi chưa dọn quá 1 giờ | > 0 kéo dài |
| `image_cleanup_parked` | Không xóa được object sau 30 lần | > 0: kiểm tra object storage |
| `author_labels_pending` | Review chưa có tên che (Identity chưa trả lời) | không giảm |
| `hidden_7d`, `reviews_24h` | Lưu lượng | — |

Log: `review_created`, `review_eligibility_unavailable` (Order lỗi), `review_image_upload_failed`, `review_image_record_failed`, `review_image_cleanup_failed`, `review_rate_limit_unavailable`. Không log nội dung review, tên hay email.

## Sự cố thường gặp

- **Order ngừng:** buyer nhận 503 khi viết review; xem review vẫn chạy. Không cần làm gì.
- **Identity ngừng:** vẫn viết và xem được; review mới hiện "Người mua" cho tới khi backfill điền tên che.
- **Object storage ngừng:** upload ảnh lỗi, review vẫn đăng (frontend báo số ảnh lỗi). Upload dở được dọn tự động khi storage lên lại; `image_cleanup_parked` > 0 thì kiểm tra quyền xóa trên bucket rồi đặt lại: `UPDATE review_image_uploads SET parked_at = NULL, attempts = 0, next_attempt_at = now() WHERE object_key = '<key>'` qua quy trình thay đổi có duyệt.
- **Ẩn nhầm:** `/admin/reviews` lọc "Đã ẩn" → "Hiển thị lại" kèm ghi chú.

## Rollback

- Tắt tính năng (D05 rollback): dừng container `review` và đặt `ADMIN_REVIEW_SERVICE_URL` rỗng (dashboard bỏ các ô review). Gateway vẫn bắt buộc `REVIEW_SERVICE_URL` nên `/api/reviews` trả lỗi upstream; trang sản phẩm chỉ còn tiêu đề phần đánh giá, checkout và thanh toán không phụ thuộc Review. Giữ dữ liệu, audit; không xóa review để gỡ tính năng.
- Image Review về bản cũ: đọc/ghi được schema mới (cột mới có default, trigger điền `entity_id`), nhưng bản cũ có lỗi không tạo được review.
- Migration down `000003` từ chối khi đã có review xác minh, audit kiểu mới hoặc upload chưa dọn.
- Dữ liệu seed: loader `fetching_data` từ chối nạp vào stack có `ENV=production`; review seed không bao giờ là "đã mua hàng".
