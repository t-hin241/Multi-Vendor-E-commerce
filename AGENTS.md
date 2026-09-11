# Agents Guide: Shopee Multi Vendor Backend

> Nguồn context DUY NHẤT cho mọi agent (Codex, Claude Code, ...). Codex đọc file này trực tiếp.
> Claude Code đọc `CLAUDE.md` (chỉ chứa `@AGENTS.md`). Không fork nội dung nghiệp vụ ra file riêng cho từng tool.

## Project Context
- Multi Vendor E-commerce backend bằng Golang; mục tiêu MVP bán hàng thật end-to-end.
- Buyer: tìm sản phẩm, giỏ hàng, checkout, thanh toán, theo dõi đơn. Vendor: đăng sản phẩm, tồn kho, xử lý đơn.
  Admin: duyệt vendor/sản phẩm, vận hành marketplace. Production đầu tiên chạy Docker trên VPS.
- Ưu tiên workflow bán hàng thật hơn tính năng trang trí; kiến trúc mặc định là microservices tối giản cho MVP,
  tránh tạo service thừa khi chưa có bounded context rõ. Mọi thay đổi phải phục vụ buyer mua được hàng, vendor xử lý được đơn, admin vận hành được.

## Context Forwarding (đa cấp thư mục, đa agent)
- Trước khi sinh code, đọc tài liệu liên quan trong `docs/`. Khi làm trong thư mục có `AGENTS.md` con
  (vd `backend/AGENTS.md`, `backend/internal/payment/AGENTS.md`), phải đọc file đó trước.
- Nếu nhiều `AGENTS.md` áp dụng: ưu tiên file gần code nhất, rồi module, rồi root này. File con được thêm rule
  riêng cho framework/module nhưng không được âm thầm phá guardrail bảo mật/payment/order/production của root.
- Nếu root và child xung đột: chọn rule an toàn hơn, hỏi lại nếu ảnh hưởng nghiệp vụ. Module/technique mới có
  rule riêng đáng nhớ nên có `AGENTS.md` con. Khi sinh code, forward rule liên quan xuống prompt/task con.
- Mọi context file dùng chung PHẢI tên `AGENTS.md` (viết hoa), kể cả ở thư mục con; nếu cần override riêng cho
  Claude Code ở thư mục đó, thêm `CLAUDE.md` cạnh nó chỉ chứa `@AGENTS.md`.

## Working Principles
- Code rõ ràng, dễ test/debug; không thêm abstraction nếu chưa giảm complexity thật; không tạo package `utils`
  mơ hồ, đặt tên theo domain/trách nhiệm thể hiện intent nghiệp vụ.
- Không duplicate business logic giữa buyer/vendor/admin API; không đưa business rules quan trọng vào frontend.
- Không dùng panic cho lỗi nghiệp vụ; lỗi nghiệp vụ có type/code rõ, lỗi hạ tầng wrap đủ ngữ cảnh; không nuốt lỗi
  từ repository/provider/transaction/background job.
- Code mới có test tương xứng rủi ro. Khi phân vân giữa nhanh và đúng payment/order logic, chọn đúng logic.

## Architecture Standards
- Microservices tối giản; mỗi service sở hữu bounded context, domain rules và dữ liệu của mình, không truy cập
  database/schema service khác nếu không qua API contract, event contract hoặc read model đã định nghĩa.
- Handler chỉ parse/validate cơ bản, gọi use case, trả response — không gọi repository trực tiếp. Use case
  orchestration workflow trong phạm vi service, chịu trách nhiệm transaction boundary.
- Domain chứa entity/value object/business rules, không phụ thuộc HTTP framework, message broker, SDK payment hay storage.
  Repository chỉ đọc/ghi dữ liệu của service hiện tại; adapter bọc tích hợp ngoài hoặc service khác.
- Cross-service workflow phải dùng API/event rõ ràng, có correlation id, timeout, retry policy và idempotency ở consumer.

## Backend Defaults
- Ngôn ngữ Go; HTTP framework Gin hoặc Echo; PostgreSQL là source of truth transactional theo từng service/schema;
  Redis cho cache, rate limit, session, Asynq jobs.
- Mỗi service có migration version hóa riêng; không sửa migration đã chạy production, tạo migration mới.
- Config đọc từ environment variables, không hard-code secret; luôn cập nhật `.env.example` khi thêm biến mới.
- Logging production có cấu trúc, mỗi request có request id; query DB và provider call phải có context timeout.

## Service Boundaries
- Identity: user/credential/role/session/password reset. Vendor: profile/onboarding/approval. Catalog:
  product/category/media/visibility. Inventory: stock/reservation/movement. Cart: giỏ hàng, chuẩn bị checkout.
- Order: order/vendor sub-order/order item/status. Payment: payment intent/transaction/webhook. Shipment:
  fulfillment state, tracking tối thiểu. Admin: moderation/audit log/operational views. Notification: gửi
  email/notification qua background job.
- Catalog không tự xử lý payment; Payment không tự quyết định order lifecycle; Notification không đổi trạng thái
  order/payment/shipment; Admin không bypass domain rules nếu không có audit flow rõ.
- Service không đọc/ghi trực tiếp database của service khác; dữ liệu đọc chéo phải qua API hoặc read model từ event.

## Identity And Access
- Role ban đầu: buyer, vendor, admin. Email unique. Password hash bằng bcrypt/argon2.
- Access token ngắn hạn; refresh token revoke được. Password reset token có hạn, dùng một lần.
- Không tin user id/vendor id/role từ client nếu chưa xác thực; mọi endpoint kiểm tra ownership/role ở
  middleware và use case. Auth errors không tiết lộ email có tồn tại hay không nếu không cần.

## Vendor And Catalog Rules
- User vendor chưa chắc đã được duyệt; chỉ bán khi approved; pending/rejected không publish lên storefront.
  Admin duyệt/từ chối vendor phải có audit log.
- Product phải thuộc đúng vendor; chỉ public khi vendor approved + product approved + active; draft/pending/
  rejected không xuất hiện storefront. Price dương, stock không âm, slug/SKU unique theo phạm vi.
- Ảnh sản phẩm validate content type/dung lượng/kích thước, lưu S3-compatible ở production. Listing/API danh
  sách luôn có pagination.

## Cart, Order And Inventory Rules
- Cart item trỏ tới sản phẩm bán được, quantity > 0, có thể nhiều vendor. Checkout tách order theo vendor, dùng
  pricing snapshot; client không gửi final price đáng tin, tổng tiền tính ở backend.
- Order item lưu giá tại thời điểm checkout, không tính lại từ giá hiện tại sau khi order đã tạo. Money dùng
  integer minor unit hoặc decimal an toàn (không float); currency explicit khi thanh toán.
- Không bán quá tồn kho; stock reservation có thời hạn; cancel/payment failed phải release reservation còn giữ.
  Race condition tồn kho xử lý bằng transaction/locking/constraint phù hợp; thay đổi stock có movement/audit.
- Order status transition phải validate; không chuyển cancelled→paid hoặc ship order chưa paid.

## Payment And Webhook Rules
- Payment provider gọi qua interface adapter; domain không import trực tiếp Stripe/PayPal SDK. Payment intent
  creation tách rõ với order creation; payment status transition phải validate.
- Webhook verify signature nếu provider hỗ trợ, phải idempotent; provider event id unique trong DB; xử lý lặp
  không tạo order/payment/transaction trùng. Payment success chỉ update order khi amount và currency khớp.
- Payment failure lưu lý do đủ debug; refund có trạng thái riêng, không chỉ ghi note. Không lưu dữ liệu thẻ
  thanh toán. Webhook log correlation order id/payment id/provider event id; endpoint cần rate limit/protection.

## Shipment, Admin And Commission
- Shipping MVP trạng thái đơn giản. Vendor chỉ cập nhật shipment vendor order của mình; buyer chỉ xem order của
  mình; vendor chỉ xem vendor order của mình; admin xem toàn bộ nhưng action nhạy cảm phải audit.
- Admin duyệt/từ chối sản phẩm ghi actor/decision/timestamp, nên có lý do khi từ chối vendor/product. Admin khóa
  vendor không được mất dữ liệu lịch sử.
- Commission MVP dùng rule phần trăm đơn giản, tính từ snapshot/rule version tại thời điểm order; không thay đổi
  commission lịch sử khi admin sửa rule mới.

## API Standards
- REST endpoint dùng noun rõ ràng, HTTP method đúng nghĩa; không dùng GET cho hành động đổi dữ liệu. Public API
  validate input chặt; validation error trả field/message rõ ràng.
- API error response format thống nhất; internal error có request id để tra log; không trả stack trace cho
  client production. Response DTO không lộ field nội bộ nhạy cảm.
- Delete destructive cân nhắc soft delete cho dữ liệu thương mại; order/payment/audit log không hard delete
  trong flow thường.

## Security And Privacy
- Không log password/token/secret/provider key/dữ liệu thẻ, không log body request chứa password/token. PII
  hạn chế log và hạn chế expose qua API; address/phone validate tối thiểu.
- Bắt buộc HTTPS production, strict CORS domain production. Rate limit cho login/register/password reset/
  checkout/webhook. Demo admin account không tồn tại mặc định ở production; seed data chỉ dùng local/staging.
  Health check không lộ dữ liệu nhạy cảm.

## Secrets And Credentials Handling
- Áp dụng cho MỌI code sinh ra: feature code, script, test, seed, docs, file config mẫu. "Secret" gồm API key,
  access/secret key, client id/secret bên thứ ba, private key, JWT signing key, webhook signing secret, connection
  string có credential, admin/seed password, token thật, ID nội bộ dùng để bypass/nâng quyền bảo mật.
- Không hard-code giá trị secret thật trong bất kỳ file commit nào (code, test, seed, migration, comment, log,
  config mẫu, hay trong nội dung trả lời). Secret chỉ đọc qua tham số cấu hình (env var/secret manager); tập
  trung việc đọc secret vào một layer config duy nhất, module khác nhận qua struct/interface đã inject.
- Thêm biến secret mới: chỉ thêm TÊN biến kèm placeholder (`CHANGE_ME`) vào `.env.example`, không ghi giá trị
  thật vào file commit; không tạo `.env` thật chứa giá trị thật trong quá trình sinh code.
- Test/fixture/seed/docs chỉ dùng giá trị giả rõ ràng, không tái dùng giá trị thật kể cả đã revoke. Không log
  giá trị secret ở bất kỳ level nào (kể cả debug); log id tham chiếu hoặc giá trị đã mask thay vì giá trị thật.
- Nếu phát hiện secret thật đã lộ trong repo/prompt/file đính kèm: dừng lại, cảnh báo người dùng, đề xuất
  rotate/thu hồi thay vì tiếp tục dùng hoặc chỉ di chuyển nó sang chỗ khác.

## Background Jobs And Cache
- Email/notification gửi qua background job; lỗi gửi email không rollback transaction order/payment chính.
  Job retry có giới hạn, idempotent; payload không chứa secret, nên chứa id tham chiếu thay vì object lớn.
- Cache không phải source of truth; cache key có namespace rõ; data nhạy cảm TTL ngắn hoặc tránh cache; không
  lưu dữ liệu duy nhất quan trọng chỉ trong Redis nếu không có persistence phù hợp.

## Testing Requirements
- CI chạy test backend + lint/format check. Unit test bắt buộc: pricing snapshot, order status transition,
  payment status transition, inventory reservation.
- Integration test bao phủ repository quan trọng; API test bao phủ auth/vendor/product/cart/checkout; webhook
  test bao phủ event lặp và signature sai; security test bao phủ buyer/vendor truy cập chéo và non-admin gọi
  endpoint admin.

## Production Readiness
- Local dev bằng Docker Compose; production MVP chạy nhiều service bằng Docker Compose trên VPS, image không hot reload, container non-root
  nếu khả thi. App có health check endpoint; migration chạy có kiểm soát trước khi nhận traffic.
- Backup database trước khi mở bán thật, restore backup thử ít nhất một lần. Monitoring lỗi payment/checkout ưu
  tiên cao. Log production ghi endpoint/status code/latency/request id đủ để tra lỗi.
- Rollback deploy phải tính khi đổi schema hoặc payment logic; không mount source code production nếu không cần.

## Phạm Vi Thay Đổi (Repo Boundary)
- Mọi thay đổi (code, file, config, migration, script, lệnh thực thi) chỉ được áp dụng bên trong thư mục gốc dự
  án này (`/shopee`), gồm mọi thư mục con (`backend/`, `frontend/`, `deploy/`, `docs/`, `.github/`, v.v.).
- Không sửa trạng thái ngoài thư mục dự án: file hệ thống, service/ứng dụng Windows khác, global git/npm/go
  config, biến môi trường hệ thống, cấu hình Docker Desktop — trừ khi user yêu cầu rõ ràng cho việc đó.
- Lệnh chẩn đoán read-only ngoài thư mục dự án (vd kiểm tra cổng đang dùng, tiến trình đang chạy) được phép để
  debug, nhưng hành động đổi trạng thái ngoài dự án (kill process, sửa registry, cài/gỡ phần mềm, đổi cấu hình
  hệ thống) phải hỏi xác nhận trước, kể cả khi cần để việc bên trong dự án chạy được.
- File tạm phục vụ chẩn đoán có thể đặt ngoài thư mục dự án (vd thư mục scratchpad của agent) nhưng không phải
  một phần của repo và không tính là thay đổi hợp lệ của dự án.

## Final Guardrails
- Không làm phức tạp hệ thống trước khi MVP end-to-end ổn định. Nếu rule ở đây xung đột với tài liệu mới hơn
  trong `docs/`, hỏi lại trước khi triển khai.
