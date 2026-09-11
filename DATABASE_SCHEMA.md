# Database Schema — Shopee Multi Vendor

Tài liệu này liệt kê toàn bộ bảng dữ liệu trong hệ thống, trích xuất trực tiếp từ các file migration (`backend/services/*/migrations/*.up.sql`).

Mỗi service sở hữu một database Postgres riêng (không service nào truy cập trực tiếp database của service khác — chỉ qua API/internal endpoint), nên bảng bên dưới được nhóm theo từng service/database. Service `admin` không sở hữu bảng nào (chỉ đọc dữ liệu qua internal API của các service khác).

---

## 1. Identity service (`identity_db`)

### `users`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| email | TEXT | NOT NULL, unique (index trên `lower(email)`) |
| password_hash | TEXT | NOT NULL |
| full_name | TEXT | NOT NULL |
| role | TEXT | NOT NULL, CHECK IN (`buyer`, `vendor`, `admin`) |
| is_active | BOOLEAN | NOT NULL, default `true` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `refresh_tokens`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| user_id | UUID | NOT NULL, FK → `users(id)` ON DELETE CASCADE |
| token_hash | TEXT | NOT NULL, unique |
| expires_at | TIMESTAMPTZ | NOT NULL |
| revoked_at | TIMESTAMPTZ | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `password_reset_tokens`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| user_id | UUID | NOT NULL, FK → `users(id)` ON DELETE CASCADE |
| token_hash | TEXT | NOT NULL, unique |
| expires_at | TIMESTAMPTZ | NOT NULL |
| used_at | TIMESTAMPTZ | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

---

## 2. Vendor service (`vendor_db`)

### `vendors`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| user_id | UUID | NOT NULL, unique |
| shop_name | TEXT | NOT NULL |
| description | TEXT | NOT NULL, default `''` |
| status | TEXT | NOT NULL, default `pending`, CHECK IN (`pending`, `approved`, `rejected`) |
| rejection_reason | TEXT | nullable |
| approved_by | UUID | nullable |
| approved_at | TIMESTAMPTZ | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `vendor_audit_logs`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| vendor_id | UUID | NOT NULL, FK → `vendors(id)` ON DELETE CASCADE |
| actor_user_id | UUID | NOT NULL |
| action | TEXT | NOT NULL, CHECK IN (`approved`, `rejected`) |
| reason | TEXT | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `vendor_addresses`
Địa chỉ kho/lấy hàng của vendor — một vendor có thể có nhiều địa chỉ, đúng một địa chỉ là mặc định (partial unique index). Thêm ở migration `000003_vendor_addresses`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| vendor_id | UUID | NOT NULL, FK → `vendors(id)` ON DELETE CASCADE |
| recipient_name | TEXT | NOT NULL |
| phone | TEXT | NOT NULL |
| province | TEXT | NOT NULL |
| district | TEXT | NOT NULL |
| ward | TEXT | NOT NULL |
| street_address | TEXT | NOT NULL |
| is_default | BOOLEAN | NOT NULL, default `false` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(vendor_id) WHERE is_default` — mỗi vendor chỉ có đúng một địa chỉ mặc định.

---

## 3. Catalog service (`catalog_db`)

### `categories`
Self-referencing 3 cấp: main-category (level 1) → category (level 2) → sub-category (level 3). `parent_id`/`level` thêm ở migration `000005_category_hierarchy`; dữ liệu cũ (trước migration) được backfill về level 1.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| name | TEXT | NOT NULL |
| slug | TEXT | NOT NULL, unique |
| parent_id | UUID | nullable, FK → `categories(id)` (self-reference) |
| level | SMALLINT | NOT NULL, default `1`, CHECK IN (`1`, `2`, `3`); CHECK `(level = 1) = (parent_id IS NULL)` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `products`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| vendor_id | UUID | NOT NULL |
| category_id | UUID | NOT NULL, FK → `categories(id)` |
| name | TEXT | NOT NULL |
| slug | TEXT | NOT NULL, unique |
| description | TEXT | NOT NULL, default `''` |
| price_amount | BIGINT | NOT NULL, CHECK `> 0` |
| currency | TEXT | NOT NULL, default `VND` |
| status | TEXT | NOT NULL, default `pending_review`, CHECK IN (`pending_review`, `approved`, `rejected`) |
| rejection_reason | TEXT | nullable |
| is_active | BOOLEAN | NOT NULL, default `true` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_images`
Ảnh chính duy nhất của sản phẩm (upload mới sẽ tự thay thế ảnh cũ).

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL, FK → `products(id)` ON DELETE CASCADE |
| object_key | TEXT | NOT NULL |
| url | TEXT | NOT NULL |
| position | INT | NOT NULL, default `0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_audit_logs`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL, FK → `products(id)` ON DELETE CASCADE |
| actor_user_id | UUID | NOT NULL |
| action | TEXT | NOT NULL, CHECK IN (`approved`, `rejected`) |
| reason | TEXT | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_media`
Thư viện media mô tả thêm sản phẩm (ảnh/video ngắn), tối đa 5 mục/sản phẩm — tách biệt với `product_images`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL, FK → `products(id)` ON DELETE CASCADE |
| kind | TEXT | NOT NULL, CHECK IN (`image`, `video`) |
| object_key | TEXT | NOT NULL |
| url | TEXT | NOT NULL |
| content_type | TEXT | NOT NULL |
| size_bytes | BIGINT | NOT NULL |
| position | INT | NOT NULL, default `0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `attributes`
Danh mục thuộc tính dùng chung cho toàn hệ thống (không thuộc riêng category nào) — category nào áp dụng thuộc tính nào do `category_attribute_rules` quyết định. Thêm ở migration `000006_attribute_management`; cột `is_variant_defining` thêm ở `000007_product_variants`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| code | TEXT | NOT NULL, unique |
| name | TEXT | NOT NULL |
| data_type | TEXT | NOT NULL, CHECK IN (`text`, `number`, `boolean`, `select`, `multi_select`) |
| unit | TEXT | nullable (vd `g`, `mm`) |
| is_variant_defining | BOOLEAN | NOT NULL, default `false` — CHECK chỉ được `true` khi `data_type = 'select'`; giá trị của thuộc tính này nằm ở `product_variant_options`, không phải `product_attribute_values` |
| is_active | BOOLEAN | NOT NULL, default `true` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

4 mã thuộc tính dành riêng cho thông tin đóng gói (seed sẵn ở migration `000009_packaging`, `is_variant_defining = false`): `pkg_weight` (g), `pkg_length`, `pkg_width`, `pkg_height` (mm). Usecase nhận diện các mã này và lưu giá trị vào `product_packaging` thay vì `product_attribute_values`.

### `attribute_options`
Danh sách lựa chọn cho thuộc tính kiểu `select`/`multi_select`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| attribute_id | UUID | NOT NULL, FK → `attributes(id)` ON DELETE CASCADE |
| value | TEXT | NOT NULL |
| position | INT | NOT NULL, default `0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(attribute_id, value)`.

### `category_attribute_rules`
Insert-only, versioned theo `(category_id, attribute_id)` — sửa rule sẽ insert version mới, không update/xoá row cũ, để `product_attribute_values.rule_id` không bị đổi nghĩa ngược. Rule được kế thừa theo cây category (main → category → sub-category); category con có thể override rule của cha.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| category_id | UUID | NOT NULL, FK → `categories(id)` ON DELETE CASCADE |
| attribute_id | UUID | NOT NULL, FK → `attributes(id)` ON DELETE CASCADE |
| version | INT | NOT NULL |
| is_required | BOOLEAN | NOT NULL, default `false` |
| is_excluded | BOOLEAN | NOT NULL, default `false` |
| position | INT | NOT NULL, default `0` |
| created_by | UUID | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(category_id, attribute_id, version)`. Index tra bản mới nhất: `(category_id, attribute_id, version DESC)`.

### `product_attribute_values`
Giá trị thuộc tính (không thuộc packaging, không phải variant-defining) của một sản phẩm — replace toàn bộ theo product (xoá hết rồi insert lại trong 1 transaction), không update từng dòng.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL, FK → `products(id)` ON DELETE CASCADE |
| attribute_id | UUID | NOT NULL, FK → `attributes(id)` |
| rule_id | UUID | nullable, FK → `category_attribute_rules(id)` — snapshot rule áp dụng lúc lưu |
| option_id | UUID | nullable, FK → `attribute_options(id)` — dùng cho `select`/`multi_select` |
| value_text | TEXT | nullable |
| value_number | NUMERIC | nullable |
| value_boolean | BOOLEAN | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_variants`
Biến thể sản phẩm (vd size/màu) — thêm ở migration `000007_product_variants`. Giá trị các thuộc tính variant-defining của biến thể nằm ở `product_variant_options`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL, FK → `products(id)` ON DELETE CASCADE |
| sku | TEXT | NOT NULL, unique |
| variant_key | TEXT | NOT NULL — khoá tổ hợp lựa chọn, unique theo `(product_id, variant_key)` (chống trùng tổ hợp) |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_variant_options`
Bảng nối: mỗi biến thể chọn đúng một `option_id` cho mỗi thuộc tính variant-defining.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| variant_id | UUID | PK (composite), FK → `product_variants(id)` ON DELETE CASCADE |
| attribute_id | UUID | PK (composite), FK → `attributes(id)` |
| option_id | UUID | NOT NULL, FK → `attribute_options(id)` |

### `product_packaging`
Thông tin đóng gói (weight/length/width/height) — 1 dòng/sản phẩm, chỉ ở cấp sản phẩm (không theo variant). Bảng riêng thay vì dùng `product_attribute_values` vì cần 4 cột số cố định để Shipment đọc trực tiếp khi tính phí ship. Required/optional theo category do `category_attribute_rules` của 4 mã `pkg_*` quyết định (không có bảng rule riêng). Thêm ở migration `000009_packaging`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| product_id | UUID | PK, FK → `products(id)` ON DELETE CASCADE |
| weight_grams | BIGINT | nullable |
| length_mm | BIGINT | nullable |
| width_mm | BIGINT | nullable |
| height_mm | BIGINT | nullable |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `vendor_name_cache`
Read-model cục bộ của Catalog, cache tên shop lấy từ Vendor service — dùng làm fallback khi Vendor service không phản hồi được lúc hiển thị storefront.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| vendor_id | UUID | PK |
| shop_name | TEXT | NOT NULL |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `product_sales_cache`
Read-model cục bộ của Catalog, cache số lượng đã bán lấy từ Order service — dùng làm fallback khi Order service không phản hồi được.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| product_id | UUID | PK |
| quantity_sold | BIGINT | NOT NULL |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `variant_stock_cache`
Read-model cục bộ của Catalog, cache tồn kho theo biến thể lấy từ Inventory service — thêm ở migration `000008_variant_stock_cache`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| variant_id | UUID | PK |
| available_quantity | BIGINT | NOT NULL |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

---

## 4. Inventory service (`inventory_db`)

### `inventory_items`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| product_id | UUID | NOT NULL |
| variant_id | UUID | nullable — thêm ở migration `000003_variant_stock`; tồn kho theo biến thể khi sản phẩm có variant |
| vendor_id | UUID | NOT NULL |
| available_quantity | BIGINT | NOT NULL, default `0`, CHECK `>= 0` |
| reserved_quantity | BIGINT | NOT NULL, default `0`, CHECK `>= 0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(product_id) WHERE variant_id IS NULL` và `(variant_id) WHERE variant_id IS NOT NULL` — một sản phẩm không-variant hoặc một biến thể chỉ có đúng một dòng tồn kho.

### `stock_reservations`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| inventory_item_id | UUID | NOT NULL, FK → `inventory_items(id)` |
| order_id | UUID | NOT NULL |
| quantity | BIGINT | NOT NULL, CHECK `> 0` |
| status | TEXT | NOT NULL, default `active`, CHECK IN (`active`, `released`, `committed`) |
| expires_at | TIMESTAMPTZ | NOT NULL |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `stock_movements`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| inventory_item_id | UUID | NOT NULL, FK → `inventory_items(id)` |
| change_quantity | BIGINT | NOT NULL (dương/âm tùy nhập/xuất kho) |
| reason | TEXT | NOT NULL |
| reference_id | TEXT | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

---

## 5. Cart service (`cart_db`)

### `carts`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| user_id | UUID | NOT NULL, unique |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `cart_items`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| cart_id | UUID | NOT NULL, FK → `carts(id)` ON DELETE CASCADE |
| product_id | UUID | NOT NULL |
| variant_id | UUID | nullable — thêm ở migration `000003_variant_items` |
| quantity | BIGINT | NOT NULL, CHECK `> 0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(cart_id, product_id) WHERE variant_id IS NULL` và `(cart_id, variant_id) WHERE variant_id IS NOT NULL` — một sản phẩm (hoặc một biến thể) chỉ có một dòng trong giỏ hàng.

---

## 6. Order service (`order_db`)

### `orders`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| buyer_id | UUID | NOT NULL |
| status | TEXT | NOT NULL, default `pending_payment`, CHECK IN (`pending_payment`, `paid`, `processing`, `shipped`, `completed`, `cancelled`, `refunded`) |
| total_amount | BIGINT | NOT NULL, CHECK `> 0` |
| currency | TEXT | NOT NULL |
| cancellation_reason | TEXT | nullable |
| recipient_name | TEXT | NOT NULL — thêm ở migration `000005_buyer_addresses_and_shipping_fee`, snapshot địa chỉ giao hàng lúc checkout |
| phone | TEXT | NOT NULL |
| province | TEXT | NOT NULL |
| district | TEXT | NOT NULL |
| ward | TEXT | NOT NULL |
| street_address | TEXT | NOT NULL |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `vendor_orders`
Đơn hàng con tách theo từng vendor từ một đơn `orders`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| order_id | UUID | NOT NULL, FK → `orders(id)` ON DELETE CASCADE |
| vendor_id | UUID | NOT NULL |
| status | TEXT | NOT NULL, default `pending_payment`, CHECK IN (`pending_payment`, `paid`, `processing`, `shipped`, `completed`, `cancelled`, `refunded`) |
| subtotal_amount | BIGINT | NOT NULL, CHECK `> 0` |
| shipping_fee_amount | BIGINT | NOT NULL, default `0` — thêm ở migration `000005_buyer_addresses_and_shipping_fee`; set một lần bằng follow-up update sau khi Shipment báo phí, cùng cơ chế snapshot với `commission_amount` |
| currency | TEXT | NOT NULL |
| commission_rate_bps | INTEGER | nullable — snapshot tại thời điểm thanh toán, thêm ở migration `000003_commission` |
| commission_amount | BIGINT | nullable — snapshot tại thời điểm thanh toán |
| net_amount | BIGINT | nullable — snapshot tại thời điểm thanh toán |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `order_items`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| order_id | UUID | NOT NULL, FK → `orders(id)` ON DELETE CASCADE |
| vendor_order_id | UUID | NOT NULL, FK → `vendor_orders(id)` ON DELETE CASCADE |
| product_id | UUID | NOT NULL |
| product_name | TEXT | NOT NULL (snapshot tên sản phẩm tại thời điểm checkout) |
| variant_id | UUID | nullable — thêm ở migration `000004_order_item_variants` |
| variant_sku | TEXT | nullable (snapshot SKU biến thể) |
| variant_label | TEXT | nullable (snapshot mô tả biến thể, vd "Đỏ / L") |
| price_amount | BIGINT | NOT NULL, CHECK `> 0` (snapshot giá) |
| quantity | BIGINT | NOT NULL, CHECK `> 0` |
| subtotal_amount | BIGINT | NOT NULL, CHECK `> 0` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `commission_rules`
Insert-only, versioned theo thời gian — không sửa/xoá rule cũ.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| rate_bps | INTEGER | NOT NULL, CHECK `0–10000` (basis points) |
| created_by | UUID | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `buyer_addresses`
Sổ địa chỉ giao hàng của buyer, đối xứng với `vendor_addresses` — một buyer có thể có nhiều địa chỉ, đúng một địa chỉ là mặc định. Thêm ở migration `000005_buyer_addresses_and_shipping_fee`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| buyer_id | UUID | NOT NULL |
| recipient_name | TEXT | NOT NULL |
| phone | TEXT | NOT NULL |
| province | TEXT | NOT NULL |
| district | TEXT | NOT NULL |
| ward | TEXT | NOT NULL |
| street_address | TEXT | NOT NULL |
| is_default | BOOLEAN | NOT NULL, default `false` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(buyer_id) WHERE is_default` — mỗi buyer chỉ có đúng một địa chỉ mặc định.

---

## 7. Payment service (`payment_db`)

### `payment_intents`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| order_id | UUID | NOT NULL |
| buyer_id | UUID | NOT NULL |
| amount | BIGINT | NOT NULL, CHECK `> 0` |
| currency | TEXT | NOT NULL |
| status | TEXT | NOT NULL, CHECK IN (`pending`, `authorized`, `captured`, `failed`, `refunded`) |
| provider | TEXT | NOT NULL |
| provider_intent_id | TEXT | NOT NULL, unique |
| failure_reason | TEXT | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `payment_events`
Một dòng cho mỗi webhook đã xử lý — khoá unique trên `provider_event_id` chính là cơ chế đảm bảo idempotent.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| provider_event_id | TEXT | NOT NULL, unique |
| payment_intent_id | UUID | NOT NULL, FK → `payment_intents(id)` ON DELETE CASCADE |
| event_type | TEXT | NOT NULL |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

---

## 8. Shipment service (`shipment_db`)

### `shipments`
Cột carrier/zone/fee/địa chỉ đích/carrier-interception thêm ở migration `000003_carriers_zones_fees` và `000004_carrier_interception` (trước đó chỉ có `carrier` dạng text tự do, `tracking_number`, `shipped_at`, `delivered_at`).

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| vendor_order_id | UUID | NOT NULL, unique (1 shipment / vendor sub-order) |
| vendor_id | UUID | NOT NULL |
| buyer_id | UUID | nullable trong schema (usecase luôn set khi tạo) — denormalize để đọc theo buyer không cần gọi cross-service |
| status | TEXT | NOT NULL, CHECK IN (`pending`, `ready_to_ship`, `shipped`, `delivered`, `cancelled`, `interception_requested`) |
| carrier_id | UUID | nullable, FK → `carriers(id)` — chọn tự động từ shipping method mặc định của vendor lúc tạo, cố định sau đó |
| tracking_number | TEXT | nullable |
| zone_id | UUID | nullable, FK → `shipping_zones(id)` — snapshot zone khớp với tỉnh/thành đích |
| zone_name | TEXT | nullable (snapshot tên zone) |
| fee_rule_id | UUID | nullable, FK → `shipping_fee_rules(id)` — snapshot rule dùng để tính phí |
| fee_amount | BIGINT | NOT NULL, default `0` — phí ship đã tính, cố định sau khi tạo |
| package_weight_grams | BIGINT | nullable — tổng khối lượng đóng gói dùng để tính phí |
| recipient_name | TEXT | nullable (snapshot địa chỉ đích) |
| phone | TEXT | nullable |
| province | TEXT | nullable |
| district | TEXT | nullable |
| ward | TEXT | nullable |
| street_address | TEXT | nullable |
| shipped_at | TIMESTAMPTZ | nullable |
| delivered_at | TIMESTAMPTZ | nullable |
| intercept_provider_ref | TEXT | nullable, unique (khi khác NULL) — mã tham chiếu yêu cầu can thiệp (intercept) phía carrier, thêm ở migration `000004_carrier_interception` |
| intercept_requested_at | TIMESTAMPTZ | nullable — thời điểm buyer huỷ đơn sau khi đã `shipped`, hệ thống gửi yêu cầu can thiệp cho carrier |
| intercept_resolved_at | TIMESTAMPTZ | nullable — thời điểm nhận được quyết định (accept/reject) từ carrier; cũng là cờ chống xử lý trùng webhook |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Index: `vendor_id`, `buyer_id`.

### `carriers`
Danh mục carrier do admin quản lý (CRUD thường, không versioned). Thêm ở migration `000003_carriers_zones_fees`.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| name | TEXT | NOT NULL |
| code | TEXT | NOT NULL, unique |
| is_active | BOOLEAN | NOT NULL, default `true` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |
| updated_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `shipping_zones`
Vùng vận chuyển do admin quản lý.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| name | TEXT | NOT NULL |
| code | TEXT | NOT NULL, unique |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

### `shipping_zone_provinces`
Ánh xạ tỉnh/thành → zone. Một tỉnh/thành chỉ thuộc đúng một zone tại một thời điểm.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| zone_id | UUID | PK (composite), FK → `shipping_zones(id)` ON DELETE CASCADE |
| province_code | TEXT | PK (composite), unique trên toàn bảng (không chỉ theo `zone_id`) |

### `shipping_fee_rules`
Insert-only, versioned theo `(carrier_id, zone_id)` — sửa rule sẽ insert version mới, không update/xoá row cũ, để shipment đã tạo trước đó giữ nguyên số liệu đã snapshot.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| carrier_id | UUID | NOT NULL, FK → `carriers(id)` ON DELETE CASCADE |
| zone_id | UUID | NOT NULL, FK → `shipping_zones(id)` ON DELETE CASCADE |
| version | INT | NOT NULL |
| base_fee_amount | BIGINT | NOT NULL, CHECK `>= 0` |
| free_weight_grams | BIGINT | NOT NULL, default `0`, CHECK `>= 0` — khối lượng miễn phí trước khi tính thêm |
| extra_fee_per_kg | BIGINT | NOT NULL, default `0`, CHECK `>= 0` — phí cộng thêm mỗi kg vượt `free_weight_grams` (làm tròn lên) |
| created_by | UUID | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(carrier_id, zone_id, version)`. Index tra bản mới nhất: `(carrier_id, zone_id, version DESC)`.

### `vendor_shipping_methods`
Carrier mà vendor bật dùng cho shop của mình — buyer không tự chọn, hệ thống luôn dùng carrier mặc định của vendor.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| vendor_id | UUID | NOT NULL (opaque, không FK cross-service) |
| carrier_id | UUID | NOT NULL, FK → `carriers(id)` ON DELETE CASCADE |
| is_default | BOOLEAN | NOT NULL, default `false` |
| is_active | BOOLEAN | NOT NULL, default `true` |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Unique index: `(vendor_id, carrier_id)` (mỗi carrier chỉ bật một lần/vendor) và `(vendor_id) WHERE is_default` (đúng một carrier mặc định/vendor).

### `shipment_tracking_events`
Audit-on-mutation: mỗi lần shipment đổi trạng thái (kể cả các bước can thiệp carrier) tự động ghi một dòng — đây là "tracking" mà buyer/vendor xem được, không phải bảng tự nhập tay.

| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| shipment_id | UUID | NOT NULL, FK → `shipments(id)` ON DELETE CASCADE |
| status | TEXT | NOT NULL (trạng thái shipment tại thời điểm ghi) |
| note | TEXT | nullable (vd lý do carrier từ chối/chấp nhận can thiệp) |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

Index: `(shipment_id, created_at)`.

---

## 9. Notification service (`notification_db`)

### `notifications`
| Cột | Kiểu dữ liệu | Ghi chú |
|---|---|---|
| id | UUID | PK, default `gen_random_uuid()` |
| user_id | UUID | NOT NULL |
| type | TEXT | NOT NULL |
| reference_id | TEXT | NOT NULL |
| status | TEXT | NOT NULL, CHECK IN (`sent`, `failed`) |
| fail_reason | TEXT | nullable |
| created_at | TIMESTAMPTZ | NOT NULL, default `now()` |

---

## 10. Admin service (`admin_db`)

Không có bảng nghiệp vụ nào — service chỉ đọc dữ liệu qua internal API của các service khác (vendor, catalog, order, ...) để phục vụ moderation/audit view, không lưu trữ dữ liệu nghiệp vụ của riêng nó. Migration `000001_init` chỉ bật extension `pgcrypto` để dự phòng cho các bảng UUID PK trong tương lai.
