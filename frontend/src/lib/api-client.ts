// Thin typed client for calling the API Gateway. All backend endpoints are
// public through the gateway, never called directly service-to-service from
// the browser.
const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  code: string;
  status: number;
  // requestId identifies the request in server logs and in the admin
  // audit (request_id), also when no response arrived.
  requestId?: string;
  constructor(status: number, code: string, message: string, requestId?: string) {
    super(message);
    this.code = code;
    this.status = status;
    this.requestId = requestId;
  }
}

// isOutcomeUnknown is true when a change may or may not have been applied:
// the response was lost (network failure, timeout) or the server failed.
// The UI should then look the operation up instead of sending it again.
export function isOutcomeUnknown(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 0 || err.status >= 500);
}

// newOperationId names one admin operation before it is sent: used as the
// request id (searchable in the audit) and as the idempotency key.
export function newOperationId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `op-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

// safeMessage is what an error may say to the user. A business refusal
// (4xx) keeps the server's message; a lost response, throttling or a
// server failure gets a fixed message with the request id to quote to
// support, never server internals.
export function safeMessage(status: number, serverMessage: string, requestId?: string): string {
  const ref = requestId ? ` Mã tra cứu: ${requestId}.` : "";
  if (status === 0) {
    return `Không nhận được phản hồi từ máy chủ; thao tác có thể đã hoặc chưa được thực hiện. Kiểm tra kết nối rồi tải lại trang.${ref}`;
  }
  if (status === 429) return "Bạn thao tác quá nhanh. Vui lòng thử lại sau ít phút.";
  if (status >= 500) return `Hệ thống đang gặp sự cố. Vui lòng thử lại sau.${ref}`;
  return serverMessage || `Yêu cầu không thành công (${status}).`;
}

type ErrorEnvelope = { error: { code: string; message: string; request_id?: string } };
type SuccessEnvelope<T> = { data: T };

type RequestOptions = {
  method?: string;
  token?: string;
  json?: unknown;
  form?: FormData;
  query?: Record<string, string | number | undefined>;
  headers?: Record<string, string>;
  // requestId is sent as X-Request-Id so the operation can be found in the
  // audit even if the response is lost.
  requestId?: string;
  // timeoutMs bounds the wait for a response (default 30s, uploads 120s).
  // A timeout is an unknown outcome (status 0), never a failure.
  timeoutMs?: number;
};

const DEFAULT_TIMEOUT_MS = 30_000;

function buildQuery(query?: Record<string, string | number | undefined>): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", token, json, form, query } = options;

  const headers: Record<string, string> = { ...options.headers };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (json !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && method !== "HEAD") headers["X-CSRF-Protection"] = "1";
  if (options.requestId) headers["X-Request-Id"] = options.requestId;

  const controller = new AbortController();
  const timer = setTimeout(
    () => controller.abort(),
    options.timeoutMs ?? (form ? 120_000 : DEFAULT_TIMEOUT_MS),
  );
  let res: Response;
  let body: SuccessEnvelope<T> | ErrorEnvelope | null;
  try {
    try {
      res = await fetch(`${API_BASE_URL}${path}${buildQuery(query)}`, {
        method,
        credentials: "include",
        headers,
        body: form ?? (json !== undefined ? JSON.stringify(json) : undefined),
        cache: "no-store",
        signal: controller.signal,
      });
    } catch {
      throw new ApiError(
        0,
        controller.signal.aborted ? "timeout" : "network_error",
        safeMessage(0, "", options.requestId),
        options.requestId,
      );
    }
    body = (await res.json().catch(() => null)) as SuccessEnvelope<T> | ErrorEnvelope | null;
  } finally {
    clearTimeout(timer);
  }

  if (!res.ok || !body || "error" in body) {
    const errBody = body && "error" in body ? body.error : null;
    const requestId = errBody?.request_id ?? res.headers.get("X-Request-Id") ?? options.requestId;
    throw new ApiError(
      res.status,
      errBody?.code ?? "unknown_error",
      safeMessage(res.status, errBody?.message ?? "", requestId ?? undefined),
      requestId ?? undefined,
    );
  }

  return body.data;
}

// ---------- Health ----------
// /healthz is a bare liveness probe, not wrapped in the {data: ...} envelope
// every other endpoint uses, so it can't go through request().

export type GatewayHealth = { status: string };

export async function fetchGatewayHealth(): Promise<GatewayHealth> {
  const res = await fetch(`${API_BASE_URL}/healthz`, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`gateway health check failed with status ${res.status}`);
  }
  return res.json() as Promise<GatewayHealth>;
}

// ---------- Auth ----------

export type Role = "buyer" | "vendor" | "admin";

export type User = {
  id: string;
  email: string;
  full_name: string;
  role: Role;
};

export type AuthResult = {
  user: User;
  access_token: string;
  access_token_expires_at: string;
  refresh_token_expires_at: string;
};

export function register(
  email: string,
  password: string,
  fullName: string,
  role: "buyer" | "vendor",
): Promise<AuthResult> {
  return request<AuthResult>("/api/auth/register", {
    method: "POST",
    json: { email, password, full_name: fullName, role },
  });
}

export function login(email: string, password: string): Promise<AuthResult> {
  return request<AuthResult>("/api/auth/login", { method: "POST", json: { email, password } });
}

export function refreshSession(): Promise<AuthResult> {
  return request<AuthResult>("/api/auth/refresh", { method: "POST" });
}

export function logout(): Promise<{ logged_out: boolean }> {
  return request("/api/auth/logout", { method: "POST" });
}

export function requestPasswordReset(email: string): Promise<{ message: string }> {
  return request("/api/auth/password-reset/request", { method: "POST", json: { email } });
}

export function confirmPasswordReset(
  token: string,
  newPassword: string,
): Promise<{ message: string }> {
  return request("/api/auth/password-reset/confirm", {
    method: "POST",
    json: { token, new_password: newPassword },
  });
}

// ---------- Admin: users ----------

export type AdminUser = {
  id: string;
  email: string;
  full_name: string;
  role: Role;
  is_active: boolean;
  created_at: string;
};

export function listUsers(
  token: string,
  params: { role?: string; q?: string; limit?: number; offset?: number } = {},
): Promise<AdminUser[]> {
  return request<AdminUser[]>("/api/auth/admin/users", { token, query: params });
}

export function setUserActive(
  token: string,
  userId: string,
  isActive: boolean,
): Promise<AdminUser> {
  return request<AdminUser>(`/api/auth/admin/users/${userId}/active`, {
    method: "PATCH",
    token,
    json: { is_active: isActive },
  });
}

// ---------- Catalog ----------

export type Category = {
  id: string;
  name: string;
  slug: string;
  // Root categories omit this field entirely (Go's `omitempty` on a nil
  // pointer), rather than sending `parent_id: null` — check with `== null`.
  parent_id?: string | null;
  level: number;
  created_at: string;
};

export type ProductImage = { id: string; url: string; position: number };

export type MediaKind = "image" | "video";

export type ProductMediaItem = {
  id: string;
  kind: MediaKind;
  url: string;
  content_type: string;
  position: number;
};

export type ProductStatus = "draft" | "pending_review" | "approved" | "rejected";

// AttributeValue is one captured attribute value on a product, as raw
// ids/values — cross-referenced against an AttributeTemplate (fetched by
// category) for display labels, rather than joined server-side.
export type AttributeValue = {
  attribute_id: string;
  option_id?: string;
  value_text?: string;
  value_number?: number;
  value_boolean?: boolean;
};

export type Product = {
  version?: number;
  selling_sync_pending?: boolean;
  id: string;
  vendor_id: string;
  category_id: string;
  name: string;
  slug: string;
  description: string;
  price_amount: number;
  currency: string;
  status: ProductStatus;
  rejection_reason?: string;
  is_active: boolean;
  images?: ProductImage[];
  media?: ProductMediaItem[];
  attributes?: AttributeValue[];
  variants?: ProductVariant[];
  // Populated by getProductForModeration, and by getProductBySlug when the
  // caller is recognized as an admin or the product's own vendor — never
  // for an anonymous buyer or an unrelated vendor. Only meaningful for a
  // non-variant product (a variant product's stock is on each of
  // `variants` instead).
  stock_quantity?: number;
  stock_info_degraded?: boolean;
  vendor_name?: string;
  quantity_sold?: number;
  created_at: string;
  updated_at: string;
};

// ---------- Attribute Management System ----------

export type AttributeDataType = "text" | "number" | "boolean" | "select" | "multi_select";

export type AttributeOption = { id: string; value: string; position: number };

export type Attribute = {
  id: string;
  code: string;
  name: string;
  data_type: AttributeDataType;
  unit?: string;
  is_active: boolean;
  is_variant_defining: boolean;
  options?: AttributeOption[];
};

// AttributeTemplateField is one field of a category's effective,
// inheritance-merged attribute template — what the vendor form renders a
// form field from for whatever category is currently selected.
// is_variant_defining marks it as a variant axis (e.g. Size, Color)
// instead of a plain descriptive field (e.g. Material).
export type AttributeTemplateField = {
  attribute_id: string;
  code: string;
  name: string;
  data_type: AttributeDataType;
  unit?: string;
  required: boolean;
  position: number;
  is_variant_defining: boolean;
  options: AttributeOption[];
};

export type AttributeTemplate = { attributes: AttributeTemplateField[] };

export type CategoryAttributeRule = {
  id: string;
  category_id: string;
  attribute_id: string;
  version: number;
  is_required: boolean;
  is_excluded: boolean;
  position: number;
};

export function getAttributeTemplate(categoryId: string): Promise<AttributeTemplate> {
  return request<AttributeTemplate>(`/api/catalog/categories/${categoryId}/attribute-template`);
}

export function listAttributes(token: string): Promise<Attribute[]> {
  return request<Attribute[]>("/api/catalog/attributes", { token });
}

export function createAttribute(
  token: string,
  code: string,
  name: string,
  dataType: AttributeDataType,
  unit?: string,
  isVariantDefining?: boolean,
): Promise<Attribute> {
  return request<Attribute>("/api/catalog/attributes", {
    method: "POST",
    token,
    json: {
      code,
      name,
      data_type: dataType,
      unit: unit ?? null,
      is_variant_defining: isVariantDefining ?? false,
    },
  });
}

// ---------- Product variants ----------

export type VariantOption = {
  attribute_id: string;
  attribute_name: string;
  option_id: string;
  option_value: string;
};

export type ProductVariant = {
  id: string;
  product_id: string;
  sku: string;
  options: VariantOption[];
  // Only populated on the public product-detail response
  // (GET /api/catalog/products/:slug) — vendor-facing variant endpoints
  // leave this unset since the vendor console has its own inventory view.
  available_quantity?: number;
  created_at: string;
};

export function createProductVariant(
  token: string,
  productId: string,
  sku: string,
  optionIds: string[],
): Promise<ProductVariant> {
  return request<ProductVariant>(`/api/catalog/products/${productId}/variants`, {
    method: "POST",
    token,
    json: { sku, option_ids: optionIds },
  });
}

export function listProductVariants(token: string, productId: string): Promise<ProductVariant[]> {
  return request<ProductVariant[]>(`/api/catalog/products/${productId}/variants`, { token });
}

// ---------- Inventory ----------

export type InventoryItem = {
  id: string;
  product_id: string;
  variant_id: string | null;
  vendor_id: string;
  available_quantity: number;
  reserved_quantity: number;
  created_at: string;
  updated_at: string;
};

export function listMyInventory(
  token: string,
  vendorId: string,
  params: { limit?: number; offset?: number } = {},
): Promise<InventoryItem[]> {
  return request<InventoryItem[]>("/api/inventory/items/mine", {
    token,
    query: { vendor_id: vendorId, ...params },
  });
}

export function createInventoryItemForProduct(
  token: string,
  productId: string,
  initialQuantity: number,
): Promise<InventoryItem> {
  return request<InventoryItem>("/api/inventory/items", {
    method: "POST",
    token,
    json: { product_id: productId, initial_quantity: initialQuantity },
  });
}

export function createInventoryItemForVariant(
  token: string,
  variantId: string,
  initialQuantity: number,
): Promise<InventoryItem> {
  return request<InventoryItem>("/api/inventory/items", {
    method: "POST",
    token,
    json: { variant_id: variantId, initial_quantity: initialQuantity },
  });
}

// RestockRequest is a vendor's ask to add more stock to a product that's
// already approved and live — it never changes available_quantity by
// itself; an admin must approve it first (see approveRestockRequest).
export type RestockStatus = "pending" | "approved" | "rejected";

export type RestockRequest = {
  id: string;
  product_id: string;
  variant_id?: string;
  vendor_id: string;
  requested_quantity: number;
  status: RestockStatus;
  rejection_reason?: string;
  created_at: string;
  decided_at?: string;
};

export function requestRestock(
  token: string,
  productId: string,
  quantity: number,
): Promise<RestockRequest> {
  return request<RestockRequest>(`/api/inventory/items/${productId}/restock`, {
    method: "PATCH",
    token,
    json: { quantity },
  });
}

export function requestRestockVariant(
  token: string,
  variantId: string,
  quantity: number,
): Promise<RestockRequest> {
  return request<RestockRequest>(`/api/inventory/items/variant/${variantId}/restock`, {
    method: "PATCH",
    token,
    json: { quantity },
  });
}

export type StockCount = {
  id: string;
  inventory_item_id: string;
  counted_on_hand: number;
  previous_available: number;
  new_available: number;
  reserved_at_count: number;
  reason: string;
  created_at: string;
};

// recordStockCount records a physical count (kiểm kê) of one stock item.
// countedOnHand includes units held for pending orders; the count can only
// lower available stock. Reuse the same countId when retrying one
// submission so it is never applied twice.
export function recordStockCount(
  token: string,
  itemId: string,
  body: { count_id: string; counted_on_hand: number; reason: string },
): Promise<StockCount> {
  return request<StockCount>(`/api/inventory/items/${itemId}/stock-counts`, {
    method: "POST",
    token,
    json: body,
  });
}

// listMyRestockRequests lets a vendor see the status of their own
// stock-increase requests — requesting one no longer has any other visible
// effect until an admin decides it.
export function listMyRestockRequests(
  token: string,
  vendorId: string,
  params: { limit?: number; offset?: number } = {},
): Promise<RestockRequest[]> {
  return request<RestockRequest[]>("/api/inventory/restock-requests/mine", {
    token,
    query: { vendor_id: vendorId, ...params },
  });
}

export function listRestockRequestsForAdmin(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<RestockRequest[]> {
  return request<RestockRequest[]>("/api/inventory/admin/restock-requests", {
    token,
    query: params,
  });
}

export function approveRestockRequest(token: string, requestId: string): Promise<RestockRequest> {
  return request<RestockRequest>(`/api/inventory/admin/restock-requests/${requestId}/approve`, {
    method: "PATCH",
    token,
  });
}

export function rejectRestockRequest(
  token: string,
  requestId: string,
  reason: string,
): Promise<RestockRequest> {
  return request<RestockRequest>(`/api/inventory/admin/restock-requests/${requestId}/reject`, {
    method: "PATCH",
    token,
    json: { reason },
  });
}

export function addAttributeOption(
  token: string,
  attributeId: string,
  value: string,
): Promise<AttributeOption> {
  return request<AttributeOption>(`/api/catalog/attributes/${attributeId}/options`, {
    method: "POST",
    token,
    json: { value },
  });
}

export function setCategoryAttributeRule(
  token: string,
  categoryId: string,
  attributeId: string,
  isRequired: boolean,
  isExcluded: boolean,
  position: number,
): Promise<CategoryAttributeRule> {
  return request<CategoryAttributeRule>(`/api/catalog/categories/${categoryId}/attribute-rules`, {
    method: "POST",
    token,
    json: { attribute_id: attributeId, is_required: isRequired, is_excluded: isExcluded, position },
  });
}

// StorefrontListing is the public product grid's response shape: the
// products themselves, plus flags telling the client when vendor-name or
// sold-count enrichment fell back to a stale cached value because Vendor
// or Order was briefly unreachable server-side.
export type StorefrontListing = {
  products: Product[];
  total: number;
  vendor_info_degraded: boolean;
  sales_info_degraded: boolean;
};

export function listCategories(): Promise<Category[]> {
  return request<Category[]>("/api/catalog/categories");
}

export function createCategory(token: string, name: string, parentId?: string): Promise<Category> {
  return request<Category>("/api/catalog/categories", {
    method: "POST",
    token,
    json: { name, parent_id: parentId ?? null },
  });
}

export function listStorefrontProducts(
  params: {
    categoryId?: string;
    vendorId?: string;
    q?: string;
    sort?: "newest" | "price_asc" | "price_desc";
    limit?: number;
    offset?: number;
  } = {},
): Promise<StorefrontListing> {
  return request<StorefrontListing>("/api/catalog/products", {
    query: {
      category_id: params.categoryId,
      vendor_id: params.vendorId,
      q: params.q,
      sort: params.sort,
      limit: params.limit,
      offset: params.offset,
    },
  });
}

// token is optional: passing the current viewer's access token (when
// logged in) lets the backend recognize an admin or the product's own
// vendor and include exact stock_quantity in the response — omitted it
// stays the same fully anonymous call as before.
export function getProductBySlug(slug: string, token?: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/${encodeURIComponent(slug)}`, { token });
}

export function createProduct(
  token: string,
  input: {
    vendorId: string;
    categoryId: string;
    name: string;
    description: string;
    priceAmount: number;
    attributes?: { attributeId: string; value?: string; optionIds?: string[] }[];
  },
): Promise<Product> {
  return request<Product>("/api/catalog/products", {
    method: "POST",
    token,
    json: {
      vendor_id: input.vendorId,
      category_id: input.categoryId,
      name: input.name,
      description: input.description,
      price_amount: input.priceAmount,
      attributes: (input.attributes ?? []).map((a) => ({
        attribute_id: a.attributeId,
        value: a.value ?? null,
        option_ids: a.optionIds ?? [],
      })),
    },
  });
}

export function listMyProducts(
  token: string,
  vendorId: string,
  params: { limit?: number; offset?: number } = {},
): Promise<Product[]> {
  return request<Product[]>("/api/catalog/products/mine", {
    token,
    query: { vendor_id: vendorId, ...params },
  });
}

// submitProductForReview moves a draft product to pending_review — the
// backend rejects this until the product has at least one image and its
// initial stock has been set up (every variant, if it has any).
export function submitProductForReview(token: string, productId: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/${productId}/submit`, {
    method: "PATCH",
    token,
  });
}

export function setProductActive(
  token: string,
  productId: string,
  isActive: boolean,
): Promise<Product> {
  return request<Product>(`/api/catalog/products/${productId}/active`, {
    method: "PATCH",
    token,
    json: { is_active: isActive },
  });
}

export function uploadProductImage(
  token: string,
  productId: string,
  file: File,
): Promise<ProductImage> {
  const form = new FormData();
  form.set("image", file);
  return request<ProductImage>(`/api/catalog/products/${productId}/images`, {
    method: "POST",
    token,
    form,
  });
}

// listProductImages returns a product's single main image (0 or 1 item):
// uploading a new one replaces whatever was there before.
export function listProductImages(token: string, productId: string): Promise<ProductImage[]> {
  return request<ProductImage[]>(`/api/catalog/products/${productId}/images`, { token });
}

// deleteProductImage removes a product's main image entirely (no
// replacement) — the corner "×" control on an already-uploaded image.
export function deleteProductImage(
  token: string,
  productId: string,
): Promise<{ deleted: boolean }> {
  return request(`/api/catalog/products/${productId}/images`, { method: "DELETE", token });
}

// listProductMedia/uploadProductMedia manage a product's extended-
// description media gallery (images or short videos) — a separate feature
// from the plain photo gallery above.
export function listProductMedia(token: string, productId: string): Promise<ProductMediaItem[]> {
  return request<ProductMediaItem[]>(`/api/catalog/products/${productId}/media`, { token });
}

export function uploadProductMedia(
  token: string,
  productId: string,
  file: File,
): Promise<ProductMediaItem> {
  const form = new FormData();
  form.set("media", file);
  return request<ProductMediaItem>(`/api/catalog/products/${productId}/media`, {
    method: "POST",
    token,
    form,
  });
}

export function listProductsForModeration(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<Product[]> {
  return request<Product[]>("/api/catalog/products/admin", { token, query: params });
}

// getProductForModeration returns the full submission behind a product row
// — images, media, variants and stock — so admin's approve/reject decision
// is informed by what the vendor was actually required to supply.
export function getProductForModeration(token: string, productId: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/admin/${productId}`, { token });
}

export function approveProduct(token: string, productId: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/admin/${productId}/approve`, {
    method: "PATCH",
    token,
  });
}

export function rejectProduct(token: string, productId: string, reason: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/admin/${productId}/reject`, {
    method: "PATCH",
    token,
    json: { reason },
  });
}

// AuditLogEntry is the shared shape of a recorded moderation decision —
// same fields whether it came from vendor_audit_logs or product_audit_logs.
export type AuditLogEntry = {
  actor_user_id: string;
  action: string;
  reason?: string;
  created_at: string;
};

export function getProductAuditLog(token: string, productId: string): Promise<AuditLogEntry[]> {
  return request<AuditLogEntry[]>(`/api/catalog/products/admin/${productId}/audit-log`, { token });
}

// ---------- Vendor ----------

export type VendorStatus = "pending" | "approved" | "rejected" | "suspended";

export type Vendor = {
  version: number;
  enforced_version: number;
  enforcement_pending: boolean;
  suspension_reason?: string;
  id: string;
  user_id: string;
  shop_name: string;
  description: string;
  status: VendorStatus;
  rejection_reason?: string;
  approved_at?: string;
  logo_url?: string;
  banner_url?: string;
  policy_text: string;
  created_at: string;
  updated_at: string;
};

// PublicVendor is the public shop page's read model — no user_id/
// rejection_reason, an anonymous visitor has no business seeing those.
export type PublicVendor = {
  id: string;
  shop_name: string;
  description: string;
  logo_url?: string;
  banner_url?: string;
  policy_text?: string;
};

export function getPublicVendorProfile(vendorId: string): Promise<PublicVendor> {
  return request<PublicVendor>(`/api/vendor/public/${vendorId}`);
}

export function applyAsVendor(
  token: string,
  shopName: string,
  description: string,
): Promise<Vendor> {
  return request<Vendor>("/api/vendor/applications", {
    method: "POST",
    token,
    json: { shop_name: shopName, description },
  });
}

// listMyVendors lists every shop (any status) the caller owns — a user may
// own several (1:N), so this backs both the shop switcher and the "My
// Shops" management page.
export async function listMyVendors(token: string): Promise<Vendor[]> {
  const shops: Vendor[] = [];
  for (let offset = 0; ; offset += 100) {
    const page = await request<Vendor[]>(`/api/vendor/mine?limit=100&offset=${offset}`, { token });
    shops.push(...page);
    if (page.length < 100) return shops;
  }
}

export function getVendor(token: string, vendorId: string): Promise<Vendor> {
  return request<Vendor>(`/api/vendor/${vendorId}`, { token });
}

export function updateVendor(
  token: string,
  vendorId: string,
  shopName: string,
  description: string,
  policyText: string,
): Promise<Vendor> {
  return request<Vendor>(`/api/vendor/${vendorId}`, {
    method: "PATCH",
    token,
    json: { shop_name: shopName, description, policy_text: policyText },
  });
}

export function uploadVendorLogo(token: string, vendorId: string, file: File): Promise<Vendor> {
  const form = new FormData();
  form.set("image", file);
  return request<Vendor>(`/api/vendor/${vendorId}/logo`, { method: "POST", token, form });
}

export function uploadVendorBanner(token: string, vendorId: string, file: File): Promise<Vendor> {
  const form = new FormData();
  form.set("image", file);
  return request<Vendor>(`/api/vendor/${vendorId}/banner`, { method: "POST", token, form });
}

export function listVendorApplications(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<Vendor[]> {
  return request<Vendor[]>("/api/vendor/admin/applications", { token, query: params });
}

export function approveVendor(token: string, vendorId: string): Promise<Vendor> {
  return request<Vendor>(`/api/vendor/admin/applications/${vendorId}/approve`, {
    method: "PATCH",
    token,
  });
}

export function rejectVendor(token: string, vendorId: string, reason: string): Promise<Vendor> {
  return request<Vendor>(`/api/vendor/admin/applications/${vendorId}/reject`, {
    method: "PATCH",
    token,
    json: { reason },
  });
}

export function getVendorAuditLog(
  token: string,
  vendorId: string,
  offset = 0,
): Promise<AuditLogEntry[]> {
  return request<AuditLogEntry[]>(
    `/api/vendor/admin/applications/${vendorId}/audit-log?limit=20&offset=${offset}`,
    {
      token,
    },
  );
}

// ---------- Cart ----------

// Why a line cannot be checked out right now; decided by the Cart service.
export type CartLineState =
  | "available"
  | "removed"
  | "under_review"
  | "not_for_sale"
  | "option_required"
  | "option_unavailable"
  | "out_of_stock"
  | "insufficient_stock"
  | "unverified";

export type CartStockStatus = "unknown" | "in_stock" | "insufficient" | "out_of_stock";

export type CartLine = {
  line_id: string;
  line_version: number;
  product_id: string;
  product_name?: string;
  variant_id?: string;
  variant_sku?: string;
  variant_label?: string;
  quantity: number;
  // Live Catalog price; null when Catalog could not be reached.
  price_amount: number | null;
  currency?: string;
  // The price the buyer last accepted for this line (display reference only).
  seen_price_amount?: number;
  seen_currency?: string;
  price_changed: boolean;
  subtotal: number | null;
  state: CartLineState;
  stock_status: CartStockStatus;
  available_quantity?: number;
  available: boolean;
};

export type Money = { amount: number; currency: string };

export type Cart = {
  version: number;
  items: CartLine[];
  // Estimate at current prices for purchasable lines; Order computes the final amount.
  subtotal: Money | null;
  total: number;
  currency?: string;
  mixed_currency: boolean;
  item_count: number;
  line_count: number;
  unavailable_lines: number;
  price_changed_lines: number;
  over_line_limit: boolean;
  checkout_ready: boolean;
  degraded: { catalog: boolean; inventory: boolean };
  limits: { max_lines: number; max_quantity_per_line: number };
  page: { limit: number; offset: number; total: number };
};

export function getCart(token: string): Promise<Cart> {
  return request<Cart>("/api/cart", { token });
}

export function addCartItem(
  token: string,
  productId: string,
  quantity: number,
  variantId?: string,
  expectedVersion?: number,
): Promise<Cart> {
  return request<Cart>("/api/cart/items", {
    method: "POST",
    token,
    json: {
      product_id: productId,
      variant_id: variantId ?? null,
      quantity,
      expected_version: expectedVersion,
    },
  });
}

export function setCartItemQuantity(
  token: string,
  productId: string,
  quantity: number,
  variantId?: string,
  expectedVersion?: number,
): Promise<Cart> {
  return request<Cart>(`/api/cart/items/${productId}`, {
    method: "PATCH",
    token,
    json: { quantity, expected_version: expectedVersion },
    query: { variant_id: variantId },
  });
}

export function removeCartItem(
  token: string,
  productId: string,
  variantId?: string,
  expectedVersion?: number,
): Promise<{ removed: boolean }> {
  return request(`/api/cart/items/${productId}`, {
    method: "DELETE",
    token,
    query: { variant_id: variantId, expected_version: expectedVersion },
  });
}

export function clearCart(token: string, expectedVersion?: number): Promise<{ cleared: boolean }> {
  return request("/api/cart", {
    method: "DELETE",
    token,
    query: { expected_version: expectedVersion },
  });
}

// Accepts the current price of the given lines exactly as the buyer was shown
// it. Cart rejects it (409 cart_changed) if the price moved again meanwhile.
export function confirmCartPrices(
  token: string,
  expectedVersion: number,
  lines: { line_id: string; price_amount: number; currency: string }[],
): Promise<Cart> {
  return request<Cart>("/api/cart/price-confirmations", {
    method: "POST",
    token,
    json: { expected_version: expectedVersion, lines },
  });
}

// ---------- Orders ----------

export type OrderStatus =
  "pending_payment" | "paid" | "processing" | "shipped" | "completed" | "cancelled" | "refunded";

export type OrderItem = {
  id: string;
  vendor_order_id?: string;
  product_id: string;
  product_name: string;
  variant_id?: string;
  variant_sku?: string;
  variant_label?: string;
  price_amount: number;
  quantity: number;
  subtotal_amount: number;
};

// Return lifecycle: buyer requests, vendor confirms, admin approves or
// rejects, the goods are received and inspected, then Payment refunds.
// "approved" means the goods may be sent back, not that money was returned.
export type ReturnStatus =
  | "requested"
  | "vendor_confirmed"
  | "rejected"
  | "approved"
  | "received"
  | "refund_pending"
  | "refunded"
  | "refund_failed";

export type ReturnRequest = {
  action_due_at?: string | null;
  waiting_on?: string;
  id: string;
  order_id: string;
  order_item_id: string;
  reason: string;
  status: ReturnStatus;
  quantity: number;
  refund_amount: number;
  policy_version: string;
  return_window_days?: number;
  evidence?: string;
  vendor_note?: string;
  decision_note?: string;
  decided_at?: string;
  received_at?: string;
  inspection_note?: string;
  restock?: boolean;
  created_at: string;
  updated_at: string;
};

export type ReturnEvent = {
  action: string;
  actor_role: string;
  from_status?: string;
  to_status: string;
  note?: string;
  created_at: string;
};

export function createReturnRequest(
  token: string,
  orderId: string,
  input: { orderItemId: string; quantity: number; reason: string; evidence?: string },
): Promise<ReturnRequest> {
  return request<ReturnRequest>(`/api/orders/${orderId}/return-requests`, {
    method: "POST",
    token,
    json: {
      order_item_id: input.orderItemId,
      quantity: input.quantity,
      reason: input.reason,
      evidence: input.evidence || undefined,
    },
  });
}

export function listMyReturns(token: string): Promise<ReturnRequest[]> {
  return request<ReturnRequest[]>("/api/orders/return-requests/mine", { token });
}

export type ShippingSnapshot = {
  fee_amount: number;
  fee_rule_id: string;
  fee_rule_version: number;
  package_weight_grams: number;
  quoted_at?: string;
};

// Commission frozen at checkout (source "checkout") or, for orders placed
// before snapshots existed, computed at payment time ("payment_time_legacy").
export type CommissionSnapshot = {
  rule_id?: string;
  rule_version?: number;
  rate_bps: number;
  base_amount: number;
  amount: number;
  net_amount: number;
  rounding?: string;
  source?: string;
};

export type VendorOrder = {
  id: string;
  order_id: string;
  vendor_id?: string;
  status: OrderStatus;
  subtotal_amount: number;
  shipping_fee_amount: number;
  refunded_amount?: number;
  currency: string;
  shipping?: ShippingSnapshot;
  commission?: CommissionSnapshot;
  commission_rate_bps?: number;
  commission_amount?: number;
  net_amount?: number;
  fulfillable?: boolean;
  completed_at?: string;
  items?: OrderItem[];
  created_at: string;
};

export type OrderRefundStatus = "requested" | "submitted" | "succeeded" | "failed" | "rejected";

export type OrderRefund = {
  id: string;
  order_id: string;
  vendor_order_id?: string;
  return_request_id?: string;
  payment_id?: string;
  reason_code: "return" | "dispute" | "late_payment" | "duplicate_payment";
  amount: number;
  currency: string;
  reason: string;
  status: OrderRefundStatus;
  failure_reason?: string;
  created_at: string;
  resolved_at?: string;
};

// A capture Payment reported for the order. "rejected" captures (late,
// duplicate, wrong amount) did not pay the order and need a refund.
export type OrderPayment = {
  payment_id: string;
  order_id: string;
  amount: number;
  currency: string;
  outcome: "applied" | "rejected";
  rejection_reason?: string;
  received_at: string;
};

export type OrderEffect = {
  id: string;
  order_id: string;
  kind: string;
  target?: string;
  status: string;
  attempts: number;
  next_attempt_at: string;
  last_error?: string;
  created_at: string;
};

export type Order = {
  id: string;
  status: OrderStatus;
  checkout_state?: "preparing" | "ready" | "failed";
  subtotal_amount?: number;
  shipping_amount?: number;
  total_amount: number;
  refunded_amount?: number;
  currency: string;
  cancellation_reason?: string;
  recipient_name: string;
  phone: string;
  province: string;
  district: string;
  ward: string;
  street_address: string;
  paid_at?: string;
  items?: OrderItem[];
  vendor_orders?: VendorOrder[];
  refunds?: OrderRefund[];
  returns?: ReturnRequest[];
  payments?: OrderPayment[];
  effects?: OrderEffect[];
  created_at: string;
  updated_at: string;
};

export type CheckoutPreviewVendor = {
  vendor_id: string;
  subtotal_amount: number;
  // null when Shipment cannot quote this shop for the address.
  shipping_fee_amount: number | null;
  shipping_error?: string;
  item_count: number;
};

export type CheckoutPreview = {
  cart_version: number;
  currency: string;
  subtotal_amount: number;
  shipping_amount: number | null;
  total_amount: number | null;
  ready: boolean;
  vendors: CheckoutPreviewVendor[];
  // Marketplace policy versions a checkout now is placed under (AF-02);
  // absent while versioned policies are off. Send policy_versions back as
  // acceptedPolicyVersions.
  policy_versions?: Record<string, number>;
  policies?: PolicySnapshot;
};

// previewCheckout prices the cart for an address with a shipping quote per
// shop. The buyer confirms total_amount; checkout is refused if it moved.
export function previewCheckout(token: string, addressId: string): Promise<CheckoutPreview> {
  return request<CheckoutPreview>("/api/orders/checkout/preview", {
    method: "POST",
    token,
    json: { address_id: addressId },
  });
}

export type CheckoutInput = {
  addressId: string;
  // The cart version the buyer reviewed; 409 cart_changed if it moved.
  cartVersion?: number;
  // The total the buyer confirmed; 409 checkout_total_changed if it moved.
  expectedTotalAmount?: number;
  // Same key for every retry of one attempt: a retry returns the same
  // order instead of creating a second one.
  idempotencyKey: string;
  // The policy versions the buyer was shown; 409 policy_changed if a new
  // version came into force meanwhile.
  acceptedPolicyVersions?: Record<string, number>;
};

export function checkout(token: string, input: CheckoutInput): Promise<Order> {
  return request<Order>("/api/orders/checkout", {
    method: "POST",
    token,
    headers: { "Idempotency-Key": input.idempotencyKey },
    json: {
      address_id: input.addressId,
      cart_version: input.cartVersion,
      expected_total_amount: input.expectedTotalAmount,
      accepted_policy_versions: input.acceptedPolicyVersions,
    },
  });
}

// ---------- Buyer addresses ----------

export type BuyerAddress = {
  id: string;
  recipient_name: string;
  phone: string;
  province: string;
  district: string;
  ward: string;
  street_address: string;
  is_default: boolean;
};

export type AddressInput = {
  recipient_name: string;
  phone: string;
  province: string;
  district: string;
  ward: string;
  street_address: string;
};

export function addBuyerAddress(token: string, input: AddressInput): Promise<BuyerAddress> {
  return request<BuyerAddress>("/api/orders/addresses", { method: "POST", token, json: input });
}

export function listBuyerAddresses(token: string): Promise<BuyerAddress[]> {
  return request<BuyerAddress[]>("/api/orders/addresses", { token });
}

export function updateBuyerAddress(
  token: string,
  addressId: string,
  input: AddressInput,
): Promise<BuyerAddress> {
  return request<BuyerAddress>(`/api/orders/addresses/${addressId}`, {
    method: "PATCH",
    token,
    json: input,
  });
}

export function deleteBuyerAddress(
  token: string,
  addressId: string,
): Promise<{ deleted: boolean }> {
  return request(`/api/orders/addresses/${addressId}`, { method: "DELETE", token });
}

export function setDefaultBuyerAddress(
  token: string,
  addressId: string,
): Promise<{ updated: boolean }> {
  return request(`/api/orders/addresses/${addressId}/default`, { method: "PATCH", token });
}

export function listMyOrders(
  token: string,
  params: { limit?: number; offset?: number } = {},
): Promise<Order[]> {
  return request<Order[]>("/api/orders/mine", { token, query: params });
}

export function getOrder(token: string, orderId: string): Promise<Order> {
  return request<Order>(`/api/orders/${orderId}`, { token });
}

export function cancelOrder(token: string, orderId: string): Promise<Order> {
  return request<Order>(`/api/orders/${orderId}/cancel`, { method: "POST", token });
}

export function listVendorOrders(
  token: string,
  vendorId: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<VendorOrder[]> {
  return request<VendorOrder[]>("/api/orders/vendor/mine", {
    token,
    query: { vendor_id: vendorId, ...params },
  });
}

export function listAdminOrders(
  token: string,
  params: {
    status?: string;
    buyer_id?: string;
    from?: string;
    to?: string;
    limit?: number;
    offset?: number;
  } = {},
): Promise<Order[]> {
  return request<Order[]>("/api/orders/admin", { token, query: params });
}

export function getAdminOrder(token: string, orderId: string): Promise<Order> {
  return request<Order>(`/api/orders/admin/${orderId}`, { token });
}

// Admin can only cancel an unpaid order here. Money goes back through
// requestOrderRefund, never by relabelling an order "refunded".
export function adminCancelOrder(token: string, orderId: string, reason: string): Promise<Order> {
  return request<Order>(`/api/orders/admin/${orderId}/transition`, {
    method: "POST",
    token,
    json: { status: "cancelled", reason },
  });
}

export type OrderRefundInput = {
  reason_code: "dispute" | "late_payment" | "duplicate_payment";
  amount: number;
  reason: string;
  vendor_order_id?: string;
  payment_id?: string;
};

// requestOrderRefund sends one refund request. operationId is reused when
// the admin resends after an unknown outcome: Order then returns the
// refund already created instead of a second one.
export function requestOrderRefund(
  token: string,
  orderId: string,
  input: OrderRefundInput,
  operationId: string,
): Promise<OrderRefund> {
  return request<OrderRefund>(`/api/orders/admin/${orderId}/refunds`, {
    method: "POST",
    token,
    json: input,
    requestId: operationId,
    headers: { "Idempotency-Key": operationId },
  });
}

export function listOrderRefunds(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<OrderRefund[]> {
  return request<OrderRefund[]>("/api/orders/admin/refunds", { token, query: params });
}

export function listPaymentExceptions(
  token: string,
  params: { limit?: number; offset?: number } = {},
): Promise<OrderPayment[]> {
  return request<OrderPayment[]>("/api/orders/admin/payment-exceptions", { token, query: params });
}

export type OrderOperations = {
  pending: number;
  parked: number;
  oldest_pending?: string;
  parked_effects: OrderEffect[];
  counts?: Record<string, number>;
  generated_at?: string;
};

export function getOrderOperations(token: string): Promise<OrderOperations> {
  return request<OrderOperations>("/api/orders/admin/operations", { token });
}

// replayOrderEffect requeues a parked effect; replayed is false when it was
// already queued or done.
export function replayOrderEffect(
  token: string,
  effectId: string,
  reason: string,
): Promise<{ replayed: boolean }> {
  return request(`/api/orders/admin/operations/effects/${effectId}/replay`, {
    method: "POST",
    token,
    json: { reason },
  });
}

export function listAdminReturns(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<ReturnRequest[]> {
  return request<ReturnRequest[]>("/api/orders/admin/return-requests", { token, query: params });
}

export function decideReturn(
  token: string,
  returnId: string,
  approve: boolean,
  note: string,
): Promise<ReturnRequest> {
  return request<ReturnRequest>(`/api/orders/admin/return-requests/${returnId}/decision`, {
    method: "POST",
    token,
    json: { approve, note },
  });
}

export function retryReturnRefund(
  token: string,
  returnId: string,
  reason: string,
): Promise<ReturnRequest> {
  return request<ReturnRequest>(`/api/orders/admin/return-requests/${returnId}/retry-refund`, {
    method: "POST",
    token,
    json: { reason },
  });
}

export function getReturnHistory(token: string, returnId: string): Promise<ReturnEvent[]> {
  return request<ReturnEvent[]>(`/api/orders/admin/return-requests/${returnId}/history`, { token });
}

export function listVendorReturns(
  token: string,
  vendorId: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<ReturnRequest[]> {
  return request<ReturnRequest[]>("/api/orders/vendor/return-requests", {
    token,
    query: { vendor_id: vendorId, ...params },
  });
}

export function confirmReturnByVendor(
  token: string,
  returnId: string,
  note: string,
): Promise<ReturnRequest> {
  return request<ReturnRequest>(`/api/orders/vendor/return-requests/${returnId}/confirm`, {
    method: "POST",
    token,
    json: { note },
  });
}

// receiveReturn records that the goods came back and were inspected; it
// starts the refund. The vendor or an admin may do it.
export function receiveReturn(
  token: string,
  as: "vendor" | "admin",
  returnId: string,
  input: { restock: boolean; note: string },
): Promise<ReturnRequest> {
  return request<ReturnRequest>(`/api/orders/${as}/return-requests/${returnId}/receive`, {
    method: "POST",
    token,
    json: input,
  });
}

export function updateVendorOrderStatus(
  token: string,
  vendorOrderId: string,
  status: "processing" | "shipped" | "completed",
): Promise<VendorOrder> {
  return request<VendorOrder>(`/api/orders/vendor/${vendorOrderId}/status`, {
    method: "PATCH",
    token,
    json: { status },
  });
}

// ---------- Vendor performance & commission ----------

export type TopProduct = {
  product_id: string;
  product_name: string;
  quantity_sold: number;
  revenue_amount: number;
};

export type VendorSummary = {
  total_orders: number;
  total_revenue: number;
  total_commission: number;
  total_net: number;
  total_refunded?: number;
  top_products: TopProduct[];
};

export function getVendorSummary(token: string, vendorId: string): Promise<VendorSummary> {
  return request<VendorSummary>("/api/orders/vendor/summary", {
    token,
    query: { vendor_id: vendorId },
  });
}

// exportVendorOrdersCSV fetches the raw CSV body directly (not through
// request(), which expects the {data: ...} JSON envelope every other
// endpoint uses) so the caller can hand it to the browser as a file download.
export async function exportVendorOrdersCSV(token: string, vendorId: string): Promise<string> {
  const res = await fetch(
    `${API_BASE_URL}/api/orders/vendor/export.csv?vendor_id=${encodeURIComponent(vendorId)}`,
    {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    },
  );
  if (!res.ok) {
    throw new ApiError(res.status, "export_failed", "Could not export orders");
  }
  return res.text();
}

export type CommissionRule = {
  id: string;
  version?: number;
  rate_bps: number;
  created_by?: string;
  created_at: string;
};

export function listCommissionRules(token: string): Promise<CommissionRule[]> {
  return request<CommissionRule[]>("/api/orders/admin/commission-rules", { token });
}

export function setCommissionRule(
  token: string,
  rateBps: number,
  reason: string,
): Promise<CommissionRule> {
  return request<CommissionRule>("/api/orders/admin/commission-rules", {
    method: "POST",
    token,
    json: { rate_bps: rateBps, reason },
  });
}

// ---------- Payments ----------

// creating: the provider link is being made; expired: the link closed
// without a payment (a late payment is still recorded as captured).
export type PaymentStatus =
  "creating" | "pending" | "authorized" | "captured" | "failed" | "refunded" | "expired";

export type PaymentIntent = {
  id: string;
  order_id: string;
  amount: number;
  currency: string;
  status: PaymentStatus;
  provider: string;
  provider_intent_id: string;
  checkout_url?: string;
  qr_code?: string;
  expires_at?: string;
  failure_reason?: string;
  created_at: string;
  updated_at: string;
};

export function createPaymentIntent(token: string, orderId: string): Promise<PaymentIntent> {
  return request<PaymentIntent>("/api/payments/intents", {
    method: "POST",
    token,
    json: { order_id: orderId },
  });
}

export function getPaymentIntent(token: string, intentId: string): Promise<PaymentIntent> {
  return request<PaymentIntent>(`/api/payments/intents/${intentId}`, { token });
}

// simulatePaymentOutcome stands in for a real hosted checkout page: this
// deployment runs the mock payment provider (see docs/phase-2), so there is
// no real card form. It exercises the exact same signature-verification and
// webhook-processing path a real provider's delivery would.
export function simulatePaymentOutcome(
  token: string,
  intentId: string,
  outcome: "succeeded" | "failed",
  failureReason?: string,
): Promise<PaymentIntent> {
  return request<PaymentIntent>(`/api/payments/intents/${intentId}/simulate`, {
    method: "POST",
    token,
    json: { outcome, failure_reason: failureReason },
  });
}

// Refunds Order requested. Payment accepts them against the capture;
// money counts as returned only once an operator records the provider or
// bank reference.
export type PaymentRefundStatus = "awaiting_provider_refund" | "pending" | "succeeded" | "failed";

export type PaymentRefund = {
  id: string;
  payment_intent_id: string;
  order_id: string;
  order_refund_id?: string;
  amount: number;
  currency: string;
  reason: string;
  status: PaymentRefundStatus;
  requested_by: string;
  evidence_reference?: string;
  note?: string;
  failure_reason?: string;
  resolved_by?: string;
  resolved_at?: string;
  created_at: string;
  updated_at: string;
};

export function listPaymentRefunds(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<PaymentRefund[]> {
  return request<PaymentRefund[]>("/api/payments/admin/refunds", { token, query: params });
}

export function resolvePaymentRefund(
  token: string,
  refundId: string,
  input: { outcome: "succeeded" | "failed"; evidence_reference?: string; note?: string },
): Promise<PaymentRefund> {
  return request<PaymentRefund>(`/api/payments/admin/refunds/${refundId}/resolve`, {
    method: "POST",
    token,
    json: input,
  });
}

// ---------- Payment reconciliation (admin) ----------

export type PaymentReceipt = {
  id: string;
  provider: string;
  provider_event_id: string;
  provider_intent_id?: string;
  provider_reference?: string;
  payment_intent_id?: string;
  event_type: string;
  amount: number;
  currency: string;
  // received | processed | retryable | rejected | parked
  status: string;
  outcome?: string;
  attempts: number;
  last_error?: string;
  received_at: string;
  processed_at?: string;
};

export type AdminPaymentIntent = PaymentIntent & {
  buyer_id: string;
  provider_reference?: string;
  create_attempts: number;
  last_error?: string;
  closed_reason?: string;
};

export type OutboxProblem = {
  payment_intent_id?: string;
  payment_refund_id?: string;
  order_id: string;
  outcome?: string;
  status?: string;
  attempts: number;
  requires_review: boolean;
  last_error?: string;
  created_at: string;
};

export type ReconciliationOverview = {
  counts: Record<string, number>;
  parked_receipts: PaymentReceipt[];
  rejected_receipts: PaymentReceipt[];
  retryable_receipts: PaymentReceipt[];
  order_sync: OutboxProblem[];
  refund_sync: OutboxProblem[];
  generated_at: string;
};

export function getPaymentReconciliation(token: string): Promise<ReconciliationOverview> {
  return request<ReconciliationOverview>("/api/payments/admin/reconciliation", { token });
}

export type PaymentSearchResult = {
  intents: AdminPaymentIntent[];
  receipts: PaymentReceipt[];
  refunds: PaymentRefund[];
};

// searchPayments looks up an order id, payment id, provider link id,
// provider reference or provider event id.
export function searchPayments(token: string, q: string): Promise<PaymentSearchResult> {
  return request<PaymentSearchResult>("/api/payments/admin/search", { token, query: { q } });
}

function adminRetry(token: string, path: string, reason: string) {
  return request<unknown>(path, { method: "POST", token, json: { reason } });
}

export function retryPaymentReceipt(token: string, receiptId: string, reason: string) {
  return adminRetry(token, `/api/payments/admin/receipts/${receiptId}/retry`, reason);
}

export function reconcilePaymentIntent(token: string, intentId: string, reason: string) {
  return adminRetry(token, `/api/payments/admin/intents/${intentId}/reconcile`, reason);
}

export function retryOrderSync(token: string, intentId: string, reason: string) {
  return adminRetry(token, `/api/payments/admin/order-sync/${intentId}/retry`, reason);
}

export function retryRefundSync(token: string, refundId: string, reason: string) {
  return adminRetry(token, `/api/payments/admin/refund-sync/${refundId}/retry`, reason);
}

// ---------- Vendor settlement and payouts (admin) ----------

export type VendorBalance = {
  vendor_id: string;
  currency: string;
  // Everything still owed to the vendor.
  owed: number;
  // Unpaid and past the return window, before Order's return/refund holds.
  eligible: number;
  in_payout: number;
  sales: number;
  commission: number;
  refunded: number;
  paid_out: number;
};

export type SettlementEntry = {
  id: string;
  vendor_id: string;
  vendor_order_id?: string;
  entry_type:
    "sale" | "shipping" | "commission" | "refund" | "commission_reversal" | "payout" | "adjustment";
  amount: number;
  currency: string;
  eligible_at: string;
  note?: string;
  created_at: string;
};

export type PayoutItem = {
  id: string;
  batch_id: string;
  vendor_id: string;
  amount: number;
  currency: string;
  destination_account_id: string;
  destination_version: number;
  destination_mask: string;
  status: "pending" | "succeeded" | "failed";
  evidence_reference?: string;
  note?: string;
  failure_reason?: string;
  resolved_at?: string;
  created_at: string;
};

export type PayoutBatch = {
  id: string;
  idempotency_key: string;
  currency: string;
  // pending: items still open; completed: every item resolved.
  status: string;
  created_by: string;
  created_at: string;
  items?: PayoutItem[];
};

export function listSettlementBalances(token: string, currency = "VND"): Promise<VendorBalance[]> {
  return request<VendorBalance[]>("/api/payments/admin/settlements/balances", {
    token,
    query: { currency, limit: 200 },
  });
}

export function listSettlementEntries(
  token: string,
  vendorId: string,
  currency = "VND",
): Promise<SettlementEntry[]> {
  return request<SettlementEntry[]>(`/api/payments/admin/settlements/vendors/${vendorId}/entries`, {
    token,
    query: { currency, limit: 100 },
  });
}

export function createSettlementAdjustment(
  token: string,
  input: { vendor_id: string; amount: number; currency: string; reason: string },
): Promise<SettlementEntry> {
  return request<SettlementEntry>("/api/payments/admin/settlements/adjustments", {
    method: "POST",
    token,
    json: input,
  });
}

export function listPayoutBatches(token: string): Promise<PayoutBatch[]> {
  return request<PayoutBatch[]>("/api/payments/admin/payouts/batches", { token });
}

export function getPayoutBatch(token: string, batchId: string): Promise<PayoutBatch> {
  return request<PayoutBatch>(`/api/payments/admin/payouts/batches/${batchId}`, { token });
}

// createPayoutBatch is idempotent by key: send the same key again after a
// timeout and the same batch comes back instead of a second one.
export function createPayoutBatch(
  token: string,
  input: { idempotency_key: string; currency: string; vendor_ids?: string[] },
): Promise<{ batch: PayoutBatch; skipped: { vendor_id: string; reason: string }[] }> {
  return request("/api/payments/admin/payouts/batches", { method: "POST", token, json: input });
}

export function resolvePayoutItem(
  token: string,
  itemId: string,
  input: { outcome: "succeeded" | "failed"; evidence_reference?: string; note?: string },
): Promise<PayoutItem> {
  return request<PayoutItem>(`/api/payments/admin/payouts/items/${itemId}/resolve`, {
    method: "POST",
    token,
    json: input,
  });
}

// ---------- Shipments ----------

// returned: the carrier brought the package back to the vendor.
export type ShipmentStatus =
  | "pending"
  | "ready_to_ship"
  | "shipped"
  | "delivered"
  | "cancelled"
  | "interception_requested"
  | "returned";

export type Shipment = {
  action_due_at?: string | null;
  waiting_on?: string;
  id: string;
  vendor_order_id: string;
  status: ShipmentStatus;
  carrier_id?: string;
  tracking_number?: string;
  zone_name?: string;
  fee_amount: number;
  package_weight_grams?: number;
  recipient_name?: string;
  phone?: string;
  province?: string;
  district?: string;
  ward?: string;
  street_address?: string;
  shipped_at?: string;
  delivered_at?: string;
  returned_at?: string;
  cancelled_at?: string;
  tracking_updated_at?: string;
  failed_attempts: number;
  last_attempt_reason?: string;
  // The buyer's contact details were removed after the retention period.
  address_redacted: boolean;
  intercept_requested_at?: string;
  intercept_resolved_at?: string;
  created_at: string;
  updated_at: string;
};

// createOrGetShipment is the vendor-triggered fallback for when the
// system's automatic checkout-time shipment creation failed — the system
// now creates shipments automatically right after checkout in the normal
// case.
export function createOrGetShipment(token: string, vendorOrderId: string): Promise<Shipment> {
  return request<Shipment>("/api/shipments", {
    method: "POST",
    token,
    json: { vendor_order_id: vendorOrderId },
  });
}

export function getShipmentByVendorOrder(token: string, vendorOrderId: string): Promise<Shipment> {
  return request<Shipment>(`/api/shipments/by-vendor-order/${vendorOrderId}`, { token });
}

// advanceShipment no longer takes a carrier: it's fixed automatically at
// creation time (already quoted and charged to the buyer), so only a
// tracking number is ever supplied here.
export function advanceShipment(
  token: string,
  shipmentId: string,
  status: "ready_to_ship" | "shipped" | "delivered" | "cancelled",
  trackingNumber?: string,
): Promise<Shipment> {
  return request<Shipment>(`/api/shipments/${shipmentId}/advance`, {
    method: "PATCH",
    token,
    json: { status, tracking_number: trackingNumber },
  });
}

// listMyShipments is shared by both roles (see ShipmentHandler.ListMine):
// a buyer sees their own shipments across every vendor, so vendorId is
// only relevant — and only sent — for the vendor-side call.
export function listMyShipments(
  token: string,
  params: { vendorId?: string; limit?: number; offset?: number } = {},
): Promise<Shipment[]> {
  return request<Shipment[]>("/api/shipments/mine", {
    token,
    query: { vendor_id: params.vendorId, limit: params.limit, offset: params.offset },
  });
}

export type TrackingEvent = {
  status: ShipmentStatus;
  note?: string;
  actor_role: "vendor" | "admin" | "carrier" | "system";
  occurred_at: string;
  created_at: string;
};

// Fulfillment actions. Each is audited; shipped/delivered/returned are
// reported to Order, which updates the order's status itself.
function shipmentAction(token: string, path: string, body: unknown = {}) {
  return request<Shipment>(path, { method: "POST", token, json: body });
}

export function markShipmentReady(token: string, shipmentId: string) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/ready`);
}

export function shipShipment(token: string, shipmentId: string, trackingNumber: string) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/ship`, {
    tracking_number: trackingNumber,
  });
}

export function updateShipmentTracking(
  token: string,
  shipmentId: string,
  trackingNumber: string,
  reason: string,
) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/tracking`, {
    tracking_number: trackingNumber,
    reason,
  });
}

export function recordFailedDelivery(token: string, shipmentId: string, reason: string) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/failed-attempts`, { reason });
}

export function markShipmentDelivered(token: string, shipmentId: string, note = "") {
  return shipmentAction(token, `/api/shipments/${shipmentId}/deliver`, { note });
}

export function markShipmentReturned(token: string, shipmentId: string, reason: string) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/return`, { reason });
}

// resolveInterception records the carrier's answer after calling it.
export function resolveInterception(
  token: string,
  shipmentId: string,
  accepted: boolean,
  note: string,
) {
  return shipmentAction(token, `/api/shipments/${shipmentId}/interception-decision`, {
    accepted,
    note,
  });
}

// ----- admin fulfillment operations -----

export type ShipmentOperations = {
  counts: Record<string, number>;
  outbox: {
    id: string;
    shipment_id: string;
    vendor_order_id: string;
    event_type: string;
    attempts: number;
    requires_review: boolean;
    last_error?: string;
    created_at: string;
  }[];
  lists: Record<string, Shipment[]>;
};

export function getShipmentOperations(token: string): Promise<ShipmentOperations> {
  return request<ShipmentOperations>("/api/shipments/admin/operations", { token });
}

export function adminShipmentAction(
  token: string,
  shipmentId: string,
  action: "deliver" | "failed-attempts" | "return" | "interception-decision",
  body: Record<string, unknown>,
): Promise<Shipment> {
  return shipmentAction(token, `/api/shipments/admin/shipments/${shipmentId}/${action}`, body);
}

export function retryShipmentOrderEvent(
  token: string,
  eventId: string,
  shipmentId: string,
  reason: string,
) {
  return request(`/api/shipments/admin/order-events/${eventId}/retry`, {
    method: "POST",
    token,
    json: { shipment_id: shipmentId, reason },
  });
}

export function listShipmentEvents(token: string, shipmentId: string): Promise<TrackingEvent[]> {
  return request<TrackingEvent[]>(`/api/shipments/${shipmentId}/events`, { token });
}

// simulateCarrierDecision stands in for a real carrier's dispatcher calling
// back with an interception decision: this deployment runs the mock
// carrier adapter, so there is no real carrier to ask. It exercises the
// exact same signature-verification and webhook-processing path a real
// carrier's delivery would, for a shipment currently "interception_requested".
export function simulateCarrierDecision(
  token: string,
  shipmentId: string,
  accepted: boolean,
  reason?: string,
): Promise<Shipment> {
  return request<Shipment>(`/api/shipments/${shipmentId}/simulate-carrier-decision`, {
    method: "POST",
    token,
    json: { accepted, reason },
  });
}

// ---------- Vendor shipping methods ----------

export type VendorShippingMethod = {
  id: string;
  carrier_id: string;
  is_default: boolean;
  is_active: boolean;
};

export function enableShippingMethod(
  token: string,
  vendorId: string,
  carrierId: string,
): Promise<VendorShippingMethod> {
  return request<VendorShippingMethod>("/api/shipments/vendor/methods", {
    method: "POST",
    token,
    json: { vendor_id: vendorId, carrier_id: carrierId },
  });
}

export function listMyShippingMethods(
  token: string,
  vendorId: string,
): Promise<VendorShippingMethod[]> {
  return request<VendorShippingMethod[]>("/api/shipments/vendor/methods", {
    token,
    query: { vendor_id: vendorId },
  });
}

export function setDefaultShippingMethod(
  token: string,
  methodId: string,
): Promise<{ updated: boolean }> {
  return request(`/api/shipments/vendor/methods/${methodId}/default`, { method: "PATCH", token });
}

export function setShippingMethodActive(
  token: string,
  methodId: string,
  isActive: boolean,
): Promise<{ updated: boolean }> {
  return request(`/api/shipments/vendor/methods/${methodId}/active`, {
    method: "PATCH",
    token,
    json: { is_active: isActive },
  });
}

// ---------- Vendor warehouse addresses ----------

export type VendorAddress = BuyerAddress;

export function addVendorAddress(
  token: string,
  vendorId: string,
  input: AddressInput,
): Promise<VendorAddress> {
  return request<VendorAddress>(`/api/vendor/${vendorId}/addresses`, {
    method: "POST",
    token,
    json: input,
  });
}

export async function listVendorAddresses(
  token: string,
  vendorId: string,
): Promise<VendorAddress[]> {
  const addresses: VendorAddress[] = [];
  for (let offset = 0; ; offset += 100) {
    const page = await request<VendorAddress[]>(
      `/api/vendor/${vendorId}/addresses?limit=100&offset=${offset}`,
      { token },
    );
    addresses.push(...page);
    if (page.length < 100) return addresses;
  }
}

export function updateVendorAddress(
  token: string,
  vendorId: string,
  addressId: string,
  input: AddressInput,
): Promise<VendorAddress> {
  return request<VendorAddress>(`/api/vendor/${vendorId}/addresses/${addressId}`, {
    method: "PATCH",
    token,
    json: input,
  });
}

export function deleteVendorAddress(
  token: string,
  vendorId: string,
  addressId: string,
): Promise<{ deleted: boolean }> {
  return request(`/api/vendor/${vendorId}/addresses/${addressId}`, { method: "DELETE", token });
}

export function setDefaultVendorAddress(
  token: string,
  vendorId: string,
  addressId: string,
): Promise<{ updated: boolean }> {
  return request(`/api/vendor/${vendorId}/addresses/${addressId}/default`, {
    method: "PATCH",
    token,
  });
}

// ---------- Admin: carriers, zones, fee rules ----------

export type Carrier = { id: string; name: string; code: string; is_active: boolean };

export function createCarrier(
  token: string,
  name: string,
  code: string,
  note = "",
): Promise<Carrier> {
  return request<Carrier>("/api/shipments/admin/carriers", {
    method: "POST",
    token,
    json: { name, code, note },
  });
}

export function listCarriers(token: string): Promise<Carrier[]> {
  return request<Carrier[]>("/api/shipments/admin/carriers", { token });
}

// listActiveCarriers is the public/vendor-facing read — only carriers
// admin has turned on, for a vendor choosing which one(s) to enable for
// their own shop.
export function listActiveCarriers(): Promise<Carrier[]> {
  return request<Carrier[]>("/api/shipments/carriers");
}

export function setCarrierActive(
  token: string,
  carrierId: string,
  isActive: boolean,
  reason: string,
): Promise<{ updated: boolean }> {
  return request(`/api/shipments/admin/carriers/${carrierId}/active`, {
    method: "PATCH",
    token,
    json: { is_active: isActive, reason },
  });
}

export type ShippingZone = { id: string; name: string; code: string };

export function createZone(
  token: string,
  name: string,
  code: string,
  note = "",
): Promise<ShippingZone> {
  return request<ShippingZone>("/api/shipments/admin/zones", {
    method: "POST",
    token,
    json: { name, code, note },
  });
}

export function listZones(token: string): Promise<ShippingZone[]> {
  return request<ShippingZone[]>("/api/shipments/admin/zones", { token });
}

export function addProvinceToZone(
  token: string,
  zoneId: string,
  provinceCode: string,
  note = "",
): Promise<{ added: boolean }> {
  return request(`/api/shipments/admin/zones/${zoneId}/provinces`, {
    method: "POST",
    token,
    json: { province_code: provinceCode, note },
  });
}

export function listZoneProvinces(token: string, zoneId: string): Promise<string[]> {
  return request<string[]>(`/api/shipments/admin/zones/${zoneId}/provinces`, { token });
}

export type FeeRule = {
  id: string;
  carrier_id: string;
  zone_id: string;
  version: number;
  base_fee_amount: number;
  free_weight_grams: number;
  extra_fee_per_kg: number;
};

export function setFeeRule(
  token: string,
  carrierId: string,
  zoneId: string,
  baseFeeAmount: number,
  freeWeightGrams: number,
  extraFeePerKg: number,
  reason: string,
): Promise<FeeRule> {
  return request<FeeRule>("/api/shipments/admin/fee-rules", {
    method: "POST",
    token,
    json: {
      carrier_id: carrierId,
      zone_id: zoneId,
      base_fee_amount: baseFeeAmount,
      free_weight_grams: freeWeightGrams,
      extra_fee_per_kg: extraFeePerKg,
      reason,
    },
  });
}

export function listFeeRules(token: string): Promise<FeeRule[]> {
  return request<FeeRule[]>("/api/shipments/admin/fee-rules", { token });
}

// ---------- Reviews ----------

export type ReviewImage = { id: string; url: string; position: number };
export type ReviewReply = { vendor_id: string; message: string; updated_at: string };
export type Review = {
  id: string;
  buyer_id?: string;
  buyer_name?: string;
  product_id: string;
  vendor_id?: string;
  order_item_id?: string;
  rating: number;
  comment: string;
  status?: "published" | "hidden";
  verified_purchase: boolean;
  images: ReviewImage[];
  reply?: ReviewReply;
  hidden_reason_id?: string;
  hidden_note?: string;
  hidden_at?: string;
  created_at: string;
};
export type ReviewSummary = {
  rating_average: number;
  rating_count: number;
  rating_distribution: [number, number, number, number, number];
};
export type ProductReviews = { reviews: Review[]; summary: ReviewSummary };
export type ReviewEligibility = {
  order_item_id: string;
  vendor_order_id: string;
  product_id: string;
  vendor_id: string;
  product_name: string;
  variant_label?: string;
  completed_at: string;
};
export type ModerationReason = {
  id: string;
  code: string;
  label: string;
  description?: string;
  is_active: boolean;
};
export type ReviewReport = {
  id: string;
  review_id: string;
  reporting_vendor_id: string;
  reason_id: string;
  reason_code: string;
  reason_label: string;
  note?: string;
  status: "open" | "resolved";
  decision?: "keep" | "hide";
  resolution_reason_id?: string;
  resolution_note?: string;
  resolved_at?: string;
  created_at: string;
};

export function listProductReviews(productId: string, rating?: number): Promise<ProductReviews> {
  return request<ProductReviews>(`/api/reviews/products/${productId}`, { query: { rating } });
}
export function listReviewEligibility(
  token: string,
  productId?: string,
): Promise<ReviewEligibility[]> {
  return request<ReviewEligibility[]>("/api/reviews/eligibility", {
    token,
    query: { product_id: productId },
  });
}
export function createReview(
  token: string,
  input: { orderItemId: string; rating: number; comment: string },
): Promise<Review> {
  return request<Review>("/api/reviews", {
    method: "POST",
    token,
    json: { order_item_id: input.orderItemId, rating: input.rating, comment: input.comment },
  });
}
export function uploadReviewImage(
  token: string,
  reviewId: string,
  file: File,
): Promise<ReviewImage> {
  const form = new FormData();
  form.append("image", file);
  return request<ReviewImage>(`/api/reviews/${reviewId}/images`, { method: "POST", token, form });
}
export function listMyReviews(token: string): Promise<Review[]> {
  return request<Review[]>("/api/reviews/mine", { token });
}
export function listVendorReviews(
  token: string,
  params: { vendorId: string; productId?: string; rating?: number; replied?: boolean },
): Promise<Review[]> {
  return request<Review[]>("/api/reviews/vendor", {
    token,
    query: {
      vendor_id: params.vendorId,
      product_id: params.productId,
      rating: params.rating,
      replied: params.replied === undefined ? undefined : String(params.replied),
    },
  });
}
export function getVendorReviewSummary(token: string, vendorId: string): Promise<ReviewSummary> {
  return request<ReviewSummary>("/api/reviews/vendor/summary", {
    token,
    query: { vendor_id: vendorId },
  });
}
export function replyToReview(
  token: string,
  reviewId: string,
  vendorId: string,
  message: string,
): Promise<ReviewReply> {
  return request<ReviewReply>(`/api/reviews/vendor/${reviewId}/reply`, {
    method: "PUT",
    token,
    json: { vendor_id: vendorId, message },
  });
}
export function listVendorModerationReasons(token: string): Promise<ModerationReason[]> {
  return request<ModerationReason[]>("/api/reviews/vendor/moderation-reasons", { token });
}
export function reportReview(
  token: string,
  reviewId: string,
  vendorId: string,
  reasonId: string,
  note?: string,
): Promise<ReviewReport> {
  return request<ReviewReport>(`/api/reviews/vendor/${reviewId}/reports`, {
    method: "POST",
    token,
    json: { vendor_id: vendorId, reason_id: reasonId, note },
  });
}
export function listAdminReviews(
  token: string,
  params: {
    buyerId?: string;
    vendorId?: string;
    productId?: string;
    status?: string;
    rating?: number;
  } = {},
): Promise<Review[]> {
  return request<Review[]>("/api/reviews/admin", {
    token,
    query: {
      buyer_id: params.buyerId,
      vendor_id: params.vendorId,
      product_id: params.productId,
      status: params.status,
      rating: params.rating,
    },
  });
}
export function hideReview(
  token: string,
  reviewId: string,
  reasonId: string,
  note: string,
): Promise<{ status: string }> {
  return request(`/api/reviews/admin/${reviewId}/hide`, {
    method: "POST",
    token,
    json: { reason_id: reasonId, note },
  });
}
export function restoreReview(
  token: string,
  reviewId: string,
  note: string,
): Promise<{ status: string }> {
  return request(`/api/reviews/admin/${reviewId}/restore`, {
    method: "POST",
    token,
    json: { note },
  });
}
export function getReviewOperations(
  token: string,
): Promise<{ counts: Record<string, number>; generated_at: string }> {
  return request("/api/reviews/admin/operations", { token });
}
export function listReviewReports(token: string, status?: string): Promise<ReviewReport[]> {
  return request<ReviewReport[]>("/api/reviews/admin/reports", { token, query: { status } });
}
export function resolveReviewReport(
  token: string,
  reportId: string,
  decision: "keep" | "hide",
  reasonId?: string,
  note?: string,
): Promise<{ resolved: boolean }> {
  return request(`/api/reviews/admin/reports/${reportId}/resolve`, {
    method: "POST",
    token,
    json: { decision, reason_id: reasonId, note },
  });
}
export function listModerationReasons(token: string): Promise<ModerationReason[]> {
  return request<ModerationReason[]>("/api/reviews/admin/moderation-reasons", { token });
}
export function createModerationReason(
  token: string,
  code: string,
  label: string,
  description?: string,
): Promise<ModerationReason> {
  return request<ModerationReason>("/api/reviews/admin/moderation-reasons", {
    method: "POST",
    token,
    json: { code, label, description },
  });
}
export function updateModerationReason(
  token: string,
  reason: ModerationReason,
): Promise<ModerationReason> {
  return request<ModerationReason>(`/api/reviews/admin/moderation-reasons/${reason.id}`, {
    method: "PATCH",
    token,
    json: reason,
  });
}

export type ProductEditor = {
  product: Product;
  attributes: AttributeValue[];
  packaging: Record<string, number | null>;
};
export function getProductEditor(token: string, productId: string): Promise<ProductEditor> {
  return request(`/api/catalog/products/${productId}/editor`, { token });
}
export function updateProductContent(
  token: string,
  productId: string,
  input: {
    version: number;
    name: string;
    description: string;
    price_amount: number;
    attributes: { attribute_id: string; value?: string; option_ids?: string[] }[];
  },
): Promise<Product> {
  return request(`/api/catalog/products/${productId}/content`, {
    token,
    method: "PATCH",
    json: input,
  });
}

export function getInventoryOperations(token: string): Promise<Record<string, number>> {
  return request<Record<string, number>>("/api/inventory/admin/operations", { token });
}
export type InventoryIssue = { order_id: string; status: string; issue: string; legacy: boolean };
export function getInventoryIssues(token: string, offset = 0): Promise<InventoryIssue[]> {
  return request<InventoryIssue[]>("/api/inventory/admin/operations/issues", {
    token,
    query: { limit: 20, offset },
  });
}
export function repairInventoryOperation(
  token: string,
  input: { order_id: string; action: string; reason: string },
): Promise<{ accepted: boolean }> {
  return request<{ accepted: boolean }>("/api/inventory/admin/operations/repair", {
    token,
    method: "POST",
    json: input,
  });
}

// ---------- Admin: operations dashboard and audit (Admin service, read-only) ----------

export type AdminSourceStatus = {
  name: string;
  status: "ok" | "unavailable";
  fetched_at?: string;
  generated_at?: string;
  error?: string;
};

// count is null when a service it depends on did not answer: never read
// an unavailable figure as zero.
export type AdminDashboardTile = {
  key: string;
  group: string;
  label: string;
  hint: string;
  link: string;
  count: number | null;
  status: "ok" | "attention" | "unavailable";
  sources: string[];
};

export type AdminDashboard = {
  generated_at: string;
  sources: AdminSourceStatus[];
  tiles: AdminDashboardTile[];
};

export function getAdminDashboard(token: string): Promise<AdminDashboard> {
  return request<AdminDashboard>("/api/admin/dashboard", { token });
}

export type AuditEntry = {
  id: string;
  source: string;
  occurred_at: string;
  actor_id?: string;
  action: string;
  entity_type: string;
  entity_id: string;
  reason?: string;
  request_id?: string;
  changes?: Record<string, unknown>;
};

export type AuditPage = {
  entries: AuditEntry[];
  next_cursor?: string;
  sources: AdminSourceStatus[];
  // false when a service did not answer: its rows are missing.
  complete: boolean;
};

export type AuditFilter = {
  source?: string;
  actor_id?: string;
  entity_type?: string;
  entity_id?: string;
  action?: string;
  request_id?: string;
  from?: string;
  to?: string;
  limit?: number;
  cursor?: string;
};

export function searchAdminAudit(token: string, filter: AuditFilter): Promise<AuditPage> {
  return request<AuditPage>("/api/admin/audit", { token, query: filter });
}

// ---------- Admin: notification delivery ----------

export type NotificationStatus = "pending" | "sending" | "sent" | "failed" | "parked";

// recipient is masked by the server (e.g. "b***@example.com").
export type AdminNotification = {
  id: string;
  event_id: string;
  source: string;
  user_id: string;
  type: string;
  template_version: string;
  reference_id: string;
  status: NotificationStatus;
  recipient?: string;
  fail_reason?: string;
  attempts: number;
  max_attempts: number;
  next_attempt_at: string;
  sent_at?: string;
  created_at: string;
  updated_at: string;
};

export type NotificationAttempt = {
  attempt: number;
  outcome: "sent" | "retry" | "failed" | "parked";
  error?: string;
  duration_ms: number;
  created_at: string;
};

export function listAdminNotifications(
  token: string,
  params: {
    status?: string;
    type?: string;
    user_id?: string;
    limit?: number;
    offset?: number;
  } = {},
): Promise<AdminNotification[]> {
  return request<AdminNotification[]>("/api/notifications/admin", { token, query: params });
}

export function getNotificationOperations(
  token: string,
): Promise<{ counts: Record<string, number> }> {
  return request("/api/notifications/admin/operations", { token });
}

export function listNotificationAttempts(
  token: string,
  id: string,
): Promise<NotificationAttempt[]> {
  return request<NotificationAttempt[]>(`/api/notifications/admin/${id}/attempts`, { token });
}

// retryNotification queues a failed or parked notification again; the
// reason is kept in the audit.
export function retryNotification(
  token: string,
  id: string,
  reason: string,
): Promise<AdminNotification> {
  return request<AdminNotification>(`/api/notifications/admin/${id}/retry`, {
    method: "POST",
    token,
    json: { reason },
  });
}

// ---------- Event bus (PLT-03) ----------

// Services that consume domain events, with their admin API prefix.
export const EVENT_CONSUMER_SERVICES = [
  { name: "order", prefix: "/api/orders/admin" },
  { name: "catalog", prefix: "/api/catalog/admin" },
  { name: "payment", prefix: "/api/payments/admin" },
  { name: "shipment", prefix: "/api/shipments/admin" },
  { name: "notification", prefix: "/api/notifications/admin" },
] as const;

export type ParkedEvent = {
  consumer: string;
  event_id: string;
  event_type: string;
  aggregate_id: string;
  attempts: number;
  last_error?: string;
  parked_at: string;
};

export function listParkedEvents(token: string, prefix: string): Promise<ParkedEvent[]> {
  return request<ParkedEvent[]>(`${prefix}/events/parked`, { token, query: { limit: 100 } });
}

export function resolveParkedEvent(
  token: string,
  prefix: string,
  consumer: string,
  eventId: string,
  action: "replay" | "discard",
  reason: string,
): Promise<{ consumer: string; event_id: string; replayed: boolean }> {
  return request(
    `${prefix}/events/${encodeURIComponent(consumer)}/${encodeURIComponent(eventId)}/${action}`,
    { method: "POST", token, json: { reason } },
  );
}

// ---------- Support cases (AF-01, Order) ----------
// One case per buyer, vendor order and topic while it is not closed. The
// case never moves money: an admin links a refund or return and the case
// follows its outcome. Buyers and vendors only ever receive public messages.

export type SupportScope = "buyer" | "vendor" | "admin";

export type SupportCategory =
  "not_received" | "missing_items" | "wrong_items" | "damaged" | "payment_issue" | "other";

export type SupportCaseStatus =
  | "open"
  | "in_progress"
  | "waiting_buyer"
  | "waiting_vendor"
  | "resolution_pending"
  | "resolved"
  | "closed";

export type SupportResolutionKind = "no_action" | "refund" | "return";

export type SupportCase = {
  id: string;
  order_id: string;
  vendor_order_id: string;
  vendor_id: string;
  buyer_id?: string;
  category: SupportCategory;
  status: SupportCaseStatus;
  assignee_id?: string;
  policy_version: string;
  due_at: string | null;
  action_due_at?: string | null;
  waiting_on?: string;
  financial_hold: boolean;
  resolution_kind: SupportResolutionKind | null;
  resolution_ref: string | null;
  resolution_note: string | null;
  resolved_at: string | null;
  closed_at: string | null;
  related_case_id?: string;
  version: number;
  created_at: string;
  updated_at: string;
};

export type SupportAttachment = { id: string; content_type: string; size_bytes: number };

export type SupportMessage = {
  id: string;
  author_id?: string;
  author_role: "buyer" | "vendor" | "admin";
  visibility: "public" | "internal";
  text: string;
  attachments: SupportAttachment[];
  created_at: string;
};

export type SupportCaseEvent = {
  id: string;
  actor_id?: string;
  actor_role: "buyer" | "vendor" | "admin" | "system";
  action: string;
  from_status: string | null;
  to_status: string;
  note?: string;
  created_at: string;
};

// The case's payout hold in Payment's ledger (PW-001), admin only.
// preparing: Payment has not confirmed it yet; needs_review: a payout
// already claimed the vendor order (or Payment refused the hold).
export type CaseHoldStatus = "preparing" | "active" | "needs_review" | "releasing" | "released";

export type SupportCaseDetail = SupportCase & {
  messages: SupportMessage[];
  events: SupportCaseEvent[];
  settlement_hold?: { status: CaseHoldStatus; note?: string; updated_at: string };
};

export type SupportCasePage = { items: SupportCase[]; next_cursor: string };

export type SupportCapability = {
  enabled: boolean;
  attachments_enabled: boolean;
  max_attachments: number;
  max_attachment_bytes: number;
  max_message_chars: number;
  reopen_window_days: number;
  poll_interval_seconds: number;
  pilot_only: boolean;
};

const SUPPORT_PREFIX: Record<SupportScope, string> = {
  buyer: "/api/orders",
  vendor: "/api/orders/vendor",
  admin: "/api/orders/admin",
};

export function getSupportCapability(
  token: string,
  scope: SupportScope = "buyer",
): Promise<SupportCapability> {
  return request<SupportCapability>(`${SUPPORT_PREFIX[scope]}/support-cases/capability`, { token });
}

export function createSupportCase(
  token: string,
  orderId: string,
  input: {
    vendorOrderId: string;
    category: SupportCategory;
    message: string;
    attachmentIds: string[];
    relatedCaseId?: string;
  },
  idempotencyKey: string,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/${orderId}/support-cases`, {
    method: "POST",
    token,
    headers: { "Idempotency-Key": idempotencyKey },
    json: {
      vendor_order_id: input.vendorOrderId,
      category: input.category,
      message: input.message,
      attachment_ids: input.attachmentIds,
      related_case_id: input.relatedCaseId || undefined,
    },
  });
}

export function listSupportCases(
  token: string,
  scope: SupportScope,
  params: {
    status?: string;
    cursor?: string;
    limit?: number;
    vendorId?: string;
    assignee?: string;
    unassigned?: boolean;
    overdue?: boolean;
  } = {},
): Promise<SupportCasePage> {
  return request<SupportCasePage>(`${SUPPORT_PREFIX[scope]}/support-cases`, {
    token,
    query: {
      status: params.status,
      cursor: params.cursor,
      limit: params.limit,
      vendor_id: params.vendorId,
      assignee: params.assignee,
      unassigned: params.unassigned ? "true" : undefined,
      overdue: params.overdue ? "true" : undefined,
    },
  });
}

export function getSupportCase(
  token: string,
  scope: SupportScope,
  caseId: string,
): Promise<SupportCaseDetail> {
  return request<SupportCaseDetail>(`${SUPPORT_PREFIX[scope]}/support-cases/${caseId}`, { token });
}

export function postSupportMessage(
  token: string,
  scope: SupportScope,
  caseId: string,
  input: { text: string; attachmentIds: string[]; visibility?: "public" | "internal" },
  idempotencyKey: string,
): Promise<SupportMessage> {
  return request<SupportMessage>(`${SUPPORT_PREFIX[scope]}/support-cases/${caseId}/messages`, {
    method: "POST",
    token,
    headers: { "Idempotency-Key": idempotencyKey },
    json: { text: input.text, attachment_ids: input.attachmentIds, visibility: input.visibility },
  });
}

export function uploadSupportAttachment(
  token: string,
  scope: SupportScope,
  file: File,
): Promise<SupportAttachment> {
  const form = new FormData();
  form.append("file", file);
  return request<SupportAttachment>(`${SUPPORT_PREFIX[scope]}/support-attachments`, {
    method: "POST",
    token,
    form,
  });
}

// fetchSupportAttachment reads a private evidence image with the caller's
// token (an <img src> cannot send it); the caller shows it from a blob URL.
export async function fetchSupportAttachment(
  token: string,
  scope: SupportScope,
  caseId: string,
  attachmentId: string,
): Promise<Blob> {
  const res = await fetch(
    `${API_BASE_URL}${SUPPORT_PREFIX[scope]}/support-cases/${caseId}/attachments/${attachmentId}`,
    { headers: { Authorization: `Bearer ${token}` }, credentials: "include", cache: "no-store" },
  );
  if (!res.ok) {
    throw new ApiError(res.status, "attachment_unavailable", "Không tải được ảnh.");
  }
  return res.blob();
}

export function reopenSupportCase(
  token: string,
  caseId: string,
  message: string,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/support-cases/${caseId}/reopen`, {
    method: "POST",
    token,
    json: { message },
  });
}

export function confirmSupportCase(token: string, caseId: string): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/support-cases/${caseId}/confirm`, {
    method: "POST",
    token,
  });
}

export function assignSupportCase(
  token: string,
  caseId: string,
  assigneeId: string,
  expectedVersion: number,
  reason: string,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/admin/support-cases/${caseId}/assignments`, {
    method: "POST",
    token,
    json: { assignee_id: assigneeId, expected_version: expectedVersion, reason },
  });
}

export function changeSupportCaseStatus(
  token: string,
  caseId: string,
  input: { status: "in_progress" | "waiting_buyer" | "waiting_vendor"; note?: string },
  expectedVersion: number,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/admin/support-cases/${caseId}/status`, {
    method: "POST",
    token,
    json: { status: input.status, note: input.note, expected_version: expectedVersion },
  });
}

export function resolveSupportCase(
  token: string,
  caseId: string,
  input: { kind: SupportResolutionKind; linkedOperationId?: string; reason: string },
  expectedVersion: number,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/admin/support-cases/${caseId}/resolutions`, {
    method: "POST",
    token,
    json: {
      resolution_kind: input.kind,
      linked_operation_id: input.linkedOperationId || undefined,
      reason: input.reason,
      expected_version: expectedVersion,
    },
  });
}

export function closeSupportCase(
  token: string,
  caseId: string,
  reason: string,
  expectedVersion: number,
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/admin/support-cases/${caseId}/close`, {
    method: "POST",
    token,
    json: { reason, expected_version: expectedVersion },
  });
}

// ---------- Policies (AF-02) ----------
// Vendor owns the text; Order snapshots the versions in force on each new
// order. Published versions never change, so a version link stays valid.

export type PolicyKind = "returns" | "shipping" | "terms" | "privacy";

export type PolicyRuleReadiness = { ready: boolean; rule_hash?: string; reason?: string };

export type MarketplacePolicy = {
  id: string;
  kind: PolicyKind;
  version: number;
  title: string;
  summary: string;
  content: string;
  contact: string;
  rule_refs: Record<string, string>;
  effective_at: string;
  content_hash: string;
  published_at?: string;
  // Admin view only.
  status?: "draft" | "preparing" | "published" | "withdrawn";
  readiness?: Record<string, PolicyRuleReadiness>;
  publication_reason?: string;
  row_version?: number;
  created_at?: string;
};

export type ShopPolicy = {
  id: string;
  vendor_id: string;
  version: number;
  content: string;
  content_hash: string;
  status: "proposed" | "approved" | "rejected";
  source: "vendor" | "legacy";
  decision_reason?: string;
  decided_at?: string;
  created_at: string;
};

export type PolicyRef = { kind: string; policy_id: string; version: number; content_hash: string };

export type PolicySnapshot = {
  source: "published" | "config";
  policies: PolicyRef[];
  returns_window_days: number;
  return_shipping_refund: string;
  return_policy_version: string;
  taken_at: string;
};

export type OrderPolicyView = {
  order_id: string;
  legacy: boolean;
  order?: PolicySnapshot;
  vendor_orders: {
    vendor_order_id: string;
    shop_policy?: PolicyRef;
    returns_window_days: number;
    return_shipping_refund: string;
    return_policy_version: string;
  }[];
};

export function listPublicPolicies(): Promise<MarketplacePolicy[]> {
  return request<MarketplacePolicy[]>("/api/vendor/public/policies");
}

export function getPublicPolicy(kind: PolicyKind): Promise<MarketplacePolicy> {
  return request<MarketplacePolicy>("/api/vendor/public/policies", { query: { kind } });
}

export function getPolicyHistory(kind: PolicyKind): Promise<MarketplacePolicy[]> {
  return request<MarketplacePolicy[]>(`/api/vendor/public/policies/${kind}/versions`);
}

export function getPolicyVersion(kind: PolicyKind, version: number): Promise<MarketplacePolicy> {
  return request<MarketplacePolicy>(`/api/vendor/public/policies/${kind}/versions/${version}`);
}

const ORDER_SCOPE_PREFIX = {
  buyer: "/api/orders",
  vendor: "/api/orders/vendor",
  admin: "/api/orders/admin",
} as const;

export function getOrderPolicySnapshot(
  token: string,
  scope: keyof typeof ORDER_SCOPE_PREFIX,
  orderId: string,
): Promise<OrderPolicyView> {
  return request<OrderPolicyView>(`${ORDER_SCOPE_PREFIX[scope]}/${orderId}/policy-snapshot`, {
    token,
  });
}

export function listAdminPolicies(
  token: string,
  params: { kind?: string; status?: string } = {},
): Promise<MarketplacePolicy[]> {
  return request<MarketplacePolicy[]>("/api/vendor/admin/policy-versions", {
    token,
    query: params,
  });
}

export type PolicyDraftInput = {
  kind: PolicyKind;
  title: string;
  summary: string;
  content: string;
  contact: string;
  rule_refs: Record<string, string>;
  effective_at: string;
};

export function createPolicyDraft(
  token: string,
  input: PolicyDraftInput,
): Promise<MarketplacePolicy> {
  return request<MarketplacePolicy>("/api/vendor/admin/policy-versions", {
    method: "POST",
    token,
    json: input,
  });
}

export function publishPolicy(
  token: string,
  policyId: string,
  expectedVersion: number,
  reason: string,
): Promise<MarketplacePolicy> {
  return request<MarketplacePolicy>(`/api/vendor/admin/policy-versions/${policyId}/publications`, {
    method: "POST",
    token,
    json: { expected_version: expectedVersion, reason },
  });
}

export function withdrawPolicy(
  token: string,
  policyId: string,
  expectedVersion: number,
  reason: string,
): Promise<MarketplacePolicy> {
  return request<MarketplacePolicy>(`/api/vendor/admin/policy-versions/${policyId}/withdrawal`, {
    method: "POST",
    token,
    json: { expected_version: expectedVersion, reason },
  });
}

export function listShopPolicyProposals(token: string, status = "proposed"): Promise<ShopPolicy[]> {
  return request<ShopPolicy[]>("/api/vendor/admin/policy-proposals", { token, query: { status } });
}

export function decideShopPolicy(
  token: string,
  proposalId: string,
  approve: boolean,
  reason: string,
): Promise<ShopPolicy> {
  return request<ShopPolicy>(`/api/vendor/admin/policy-proposals/${proposalId}/decisions`, {
    method: "POST",
    token,
    json: { approve, reason },
  });
}

export function proposeShopPolicy(
  token: string,
  vendorId: string,
  content: string,
): Promise<ShopPolicy> {
  return request<ShopPolicy>(`/api/vendor/${vendorId}/policy-proposals`, {
    method: "POST",
    token,
    json: { content },
  });
}

export function listMyShopPolicies(token: string, vendorId: string): Promise<ShopPolicy[]> {
  return request<ShopPolicy[]>(`/api/vendor/${vendorId}/policy-proposals`, { token });
}

// ---------- Shop staff (AF-17) ----------
// Vendor owns shop memberships; the console lists the shops the signed-in
// person may open from these endpoints, never from the token's role.

export type ShopRole = "owner" | "staff";

export type AccessibleShop = {
  vendor_id: string;
  shop_name: string;
  status: VendorStatus;
  logo_url?: string;
  role: ShopRole;
  capabilities: string[];
  membership_version: number;
};

export type PermissionDefinition = { name: string; owner_only: boolean; available: boolean };

export type ShopMember = {
  vendor_id: string;
  user_id: string;
  email?: string;
  role: ShopRole;
  status: "active" | "revoked";
  version: number;
  permissions: string[];
  created_at: string;
  revoked_at?: string;
};

export type StaffInvitation = {
  id: string;
  email_hint: string;
  permissions: string[];
  status: "pending" | "accepted" | "revoked" | "superseded";
  expired: boolean;
  delivery_status: "queued" | "sent" | "parked" | "closed";
  invited_by: string;
  expires_at: string;
  accepted_at?: string;
  created_at: string;
};

// MemberShop is GET /api/vendor/:id for any member: the shop plus the
// caller's role and capabilities in it.
export type MemberShop = Vendor & {
  role: ShopRole;
  capabilities: string[];
  membership_version: number;
};

export function listAccessibleShops(
  token: string,
): Promise<{ shops: AccessibleShop[]; staff_enabled: boolean }> {
  return request("/api/vendor/accessible-shops", { token });
}

export function getMemberShop(token: string, vendorId: string): Promise<MemberShop> {
  return request<MemberShop>(`/api/vendor/${vendorId}`, { token });
}

export function listStaffPermissions(
  token: string,
): Promise<{ permissions: PermissionDefinition[]; enabled: boolean }> {
  return request("/api/vendor/staff-permissions", { token });
}

export function inviteStaff(
  token: string,
  vendorId: string,
  email: string,
  permissions: string[],
): Promise<StaffInvitation> {
  return request<StaffInvitation>(`/api/vendor/${vendorId}/staff-invitations`, {
    method: "POST",
    token,
    json: { email, permissions },
  });
}

export function listStaffInvitations(token: string, vendorId: string): Promise<StaffInvitation[]> {
  return request<StaffInvitation[]>(`/api/vendor/${vendorId}/staff-invitations`, { token });
}

export function revokeStaffInvitation(
  token: string,
  vendorId: string,
  invitationId: string,
  reason?: string,
): Promise<{ revoked: boolean }> {
  return request(`/api/vendor/${vendorId}/staff-invitations/${invitationId}`, {
    method: "DELETE",
    token,
    json: reason ? { reason } : {},
  });
}

// The token travels in the body only (never a URL a log could keep).
export function acceptStaffInvitation(token: string, invitationToken: string): Promise<ShopMember> {
  return request<ShopMember>("/api/vendor/staff-invitations/accept", {
    method: "POST",
    token,
    json: { token: invitationToken },
  });
}

export function listShopMembers(token: string, vendorId: string): Promise<ShopMember[]> {
  return request<ShopMember[]>(`/api/vendor/${vendorId}/members`, { token });
}

export function updateShopMember(
  token: string,
  vendorId: string,
  userId: string,
  permissions: string[],
  expectedVersion: number,
  reason?: string,
): Promise<ShopMember> {
  return request<ShopMember>(`/api/vendor/${vendorId}/members/${userId}`, {
    method: "PATCH",
    token,
    json: { permissions, expected_version: expectedVersion, ...(reason ? { reason } : {}) },
  });
}

export function removeShopMember(
  token: string,
  vendorId: string,
  userId: string,
  expectedVersion: number,
  reason?: string,
): Promise<{ removed: boolean }> {
  return request(`/api/vendor/${vendorId}/members/${userId}`, {
    method: "DELETE",
    token,
    json: { expected_version: expectedVersion, ...(reason ? { reason } : {}) },
  });
}

// ---------- Scoped admin permissions and approvals (AF-19) ----------
// Identity holds each admin's permission bundles; Payment holds
// maker-checker requests for manual money actions. The console only hides
// what an admin cannot use; every service checks again.

export type AdminPermissions = {
  permissions: string[];
  permission_version: number;
  scoped: boolean;
  bundles: string[];
};

export function getMyAdminPermissions(token: string): Promise<AdminPermissions> {
  return request<AdminPermissions>("/api/auth/permissions", { token });
}

export type PermissionGrant = {
  id: string;
  user_id: string;
  bundle: string;
  status: "active" | "revoked";
  granted_by?: string;
  reason: string;
  created_at: string;
  revoked_by?: string;
  revoke_reason?: string;
  revoked_at?: string;
};

export type PermissionSubject = {
  user: AdminUser & { permission_version: number };
  grants: PermissionGrant[];
};

export function listPermissionSubjects(token: string): Promise<PermissionSubject[]> {
  return request<PermissionSubject[]>("/api/auth/admin/permission-subjects", { token });
}

export function grantAdminPermission(
  token: string,
  input: { subject_id: string; bundle: string; reason: string; expected_version: number },
): Promise<PermissionGrant> {
  return request<PermissionGrant>("/api/auth/admin/permission-grants", {
    method: "POST",
    token,
    json: input,
  });
}

export function revokeAdminPermission(
  token: string,
  grantId: string,
  reason: string,
  expectedVersion: number,
): Promise<{ revoked: boolean }> {
  return request(`/api/auth/admin/permission-grants/${grantId}`, {
    method: "DELETE",
    token,
    json: { reason, expected_version: expectedVersion },
  });
}

// reauthenticate re-checks the password for one operation and returns a
// one-time proof valid for five minutes. The password is never stored.
export function reauthenticate(
  token: string,
  input: { password: string; purpose: string; operation_hash: string },
): Promise<{ proof: string; expires_at: string }> {
  return request("/api/auth/reauthentications", { method: "POST", token, json: input });
}

export type ApprovalKind = "refund_resolution" | "payout_item_resolution" | "settlement_adjustment";
export type ApprovalStatus =
  "draft" | "pending" | "approved" | "rejected" | "expired" | "cancelled";

export type ApprovalRequest = {
  id: string;
  operation_kind: ApprovalKind;
  target_id: string;
  payload: Record<string, unknown>;
  payload_hash: string;
  snapshot: Record<string, unknown>;
  status: ApprovalStatus;
  maker_id: string;
  maker_permission_version: number;
  reason: string;
  checker_id?: string;
  checker_permission_version?: number;
  decision_reason?: string;
  version: number;
  expires_at: string;
  created_at: string;
  submitted_at?: string;
  decided_at?: string;
  execution_ref?: string;
};

export function listApprovalRequests(
  token: string,
  status = "",
): Promise<{ items: ApprovalRequest[]; enabled: boolean }> {
  return request("/api/payments/admin/approval-requests", {
    token,
    query: { status: status || undefined, limit: 100 },
  });
}

export function createApprovalRequest(
  token: string,
  input: { operation_kind: ApprovalKind; target_id: string; payload: unknown; reason: string },
): Promise<ApprovalRequest> {
  return request<ApprovalRequest>("/api/payments/admin/approval-requests", {
    method: "POST",
    token,
    json: input,
  });
}

export function submitApprovalRequest(
  token: string,
  id: string,
  proof: string,
  expectedVersion: number,
): Promise<ApprovalRequest> {
  return request<ApprovalRequest>(`/api/payments/admin/approval-requests/${id}/submission`, {
    method: "POST",
    token,
    json: { proof, expected_version: expectedVersion },
  });
}

export function decideApprovalRequest(
  token: string,
  id: string,
  input: {
    decision: "approve" | "reject";
    proof: string;
    expected_version: number;
    reason: string;
  },
): Promise<ApprovalRequest> {
  return request<ApprovalRequest>(`/api/payments/admin/approval-requests/${id}/decisions`, {
    method: "POST",
    token,
    json: input,
  });
}

export function cancelApprovalRequest(
  token: string,
  id: string,
  expectedVersion: number,
): Promise<ApprovalRequest> {
  return request<ApprovalRequest>(`/api/payments/admin/approval-requests/${id}/cancellation`, {
    method: "POST",
    token,
    json: { expected_version: expectedVersion },
  });
}

// ----- AF-06 manual bank-transfer refunds -----

// Where a buyer's refund stands. "processing" covers verified, claimed and
// submitted transfers: money counts as back only once "refunded".
export type BuyerRefundStage =
  | "awaiting_destination"
  | "destination_rejected"
  | "verifying"
  | "processing"
  | "refunded"
  | "failed";

export type RefundDestinationSummary = {
  version: number;
  masked: string;
  status: "pending_verification" | "verified" | "rejected" | "superseded";
  submitted_at: string;
  decided_at?: string;
  decision_reason?: string;
};

export type BuyerRefund = {
  id: string;
  order_id: string;
  order_refund_id?: string;
  vendor_order_id?: string;
  amount: number;
  currency: string;
  status: PaymentRefundStatus;
  stage: BuyerRefundStage;
  destination?: RefundDestinationSummary;
  can_submit_destination: boolean;
  timeline: { event: string; at: string }[];
  created_at: string;
  resolved_at?: string;
};

export function listMyRefunds(token: string, orderId?: string): Promise<BuyerRefund[]> {
  return request<BuyerRefund[]>("/api/payments/refunds", {
    token,
    query: orderId ? { order_id: orderId } : {},
  });
}

export function submitRefundDestination(
  token: string,
  refundId: string,
  input: {
    bank_code: string;
    account_number: string;
    account_name: string;
    expected_version: number;
  },
): Promise<BuyerRefund> {
  return request<BuyerRefund>(`/api/payments/refunds/${refundId}/beneficiary`, {
    method: "POST",
    token,
    json: input,
  });
}

export type ManualRefundStage =
  | "awaiting_destination"
  | "verifying"
  | "ready"
  | "executing"
  | "submitted"
  | "unknown"
  | "confirmed"
  | "failed";

export type RefundAttemptStage =
  "ready" | "executing" | "submitted" | "confirmed" | "failed" | "unknown" | "voided";

export type RefundEvidence = {
  id: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  uploaded_by: string;
  state: string;
  created_at: string;
};

export type RefundAttempt = {
  id: string;
  refund_id: string;
  destination_version: number;
  amount: number;
  currency: string;
  stage: RefundAttemptStage;
  version: number;
  prepared_by: string;
  prepare_reason: string;
  claimed_by?: string;
  claimed_at?: string;
  lease_expires_at?: string;
  source_account?: string;
  bank_reference?: string;
  executed_at?: string;
  submitted_by?: string;
  submitted_at?: string;
  decided_by?: string;
  decided_at?: string;
  decision_reason?: string;
  evidence: RefundEvidence[];
  created_at: string;
  updated_at: string;
};

export type ManualRefundDetail = {
  refund: PaymentRefund;
  stage: ManualRefundStage;
  destinations: (RefundDestinationSummary & {
    id: string;
    key_version: number;
    submitted_by: string;
    decided_by?: string;
  })[];
  attempts: RefundAttempt[];
  audit: { actor_id: string; action: string; reason: string; created_at: string }[];
};

export function getManualRefund(token: string, refundId: string): Promise<ManualRefundDetail> {
  return request<ManualRefundDetail>(`/api/payments/admin/refunds/${refundId}/manual`, { token });
}

export function decideRefundDestination(
  token: string,
  refundId: string,
  input: {
    destination_version: number;
    decision: "verify" | "reject";
    reason: string;
    proof?: string;
  },
): Promise<{ stage: ManualRefundStage }> {
  return request(`/api/payments/admin/refunds/${refundId}/destination-decisions`, {
    method: "POST",
    token,
    json: input,
  });
}

export type RevealedRefundDestination = {
  destination_version: number;
  bank_code: string;
  account_number: string;
  account_name: string;
};

export function revealRefundDestination(
  token: string,
  refundId: string,
  input: { reason: string; proof?: string },
): Promise<RevealedRefundDestination> {
  return request<RevealedRefundDestination>(
    `/api/payments/admin/refunds/${refundId}/sensitive-access`,
    { method: "POST", token, json: input },
  );
}

export function prepareRefundAttempt(
  token: string,
  refundId: string,
  input: { destination_version: number; reason: string },
): Promise<RefundAttempt> {
  return request<RefundAttempt>(`/api/payments/admin/refunds/${refundId}/manual-attempts`, {
    method: "POST",
    token,
    json: input,
  });
}

function attemptAction<T>(token: string, attemptId: string, action: string, json: unknown) {
  return request<T>(`/api/payments/admin/refund-attempts/${attemptId}/${action}`, {
    method: "POST",
    token,
    json,
  });
}

export function claimRefundAttempt(token: string, attemptId: string, expectedVersion: number) {
  return attemptAction<RefundAttempt>(token, attemptId, "claims", {
    expected_version: expectedVersion,
  });
}

export function cancelRefundAttempt(
  token: string,
  attemptId: string,
  input: { expected_version: number; reason: string },
) {
  return attemptAction<RefundAttempt>(token, attemptId, "cancellation", input);
}

export function submitRefundAttempt(
  token: string,
  attemptId: string,
  input: {
    expected_version: number;
    bank_reference: string;
    source_account: string;
    executed_at: string;
    evidence_ids: string[];
  },
) {
  return attemptAction<RefundAttempt>(token, attemptId, "submissions", input);
}

export function decideRefundAttempt(
  token: string,
  attemptId: string,
  input: { expected_version: number; decision: "confirm" | "fail"; reason: string; proof?: string },
) {
  return attemptAction<RefundAttempt>(token, attemptId, "decisions", input);
}

export function uploadRefundEvidence(
  token: string,
  attemptId: string,
  file: File,
): Promise<RefundEvidence> {
  const form = new FormData();
  form.append("file", file);
  return request<RefundEvidence>(`/api/payments/admin/refund-attempts/${attemptId}/evidence`, {
    method: "POST",
    token,
    form,
  });
}

// fetchRefundEvidence reads a private transfer receipt with the caller's
// token; the caller opens it from a blob URL.
export async function fetchRefundEvidence(token: string, evidenceId: string): Promise<Blob> {
  const res = await fetch(`${API_BASE_URL}/api/payments/admin/refund-evidence/${evidenceId}`, {
    headers: { Authorization: `Bearer ${token}` },
    credentials: "include",
    cache: "no-store",
  });
  if (!res.ok) {
    throw new ApiError(res.status, "evidence_unavailable", "Could not load the evidence file.");
  }
  return res.blob();
}

// ----- AF-01 completion: refund from a case (PW-014), intakes (PW-012) -----

export type CaseRefundResult = {
  case: SupportCase;
  refund_id: string;
  refund_status: OrderRefundStatus;
  amount: number;
  currency: string;
};

// createCaseRefund opens a dispute refund on the case's vendor order and
// links it as the pending resolution. idempotencyKey is reused on a retry.
export function createCaseRefund(
  token: string,
  caseId: string,
  input: { amount: number; reason: string; expected_version: number },
  idempotencyKey: string,
): Promise<CaseRefundResult> {
  return request<CaseRefundResult>(`/api/orders/admin/support-cases/${caseId}/refunds`, {
    method: "POST",
    token,
    headers: { "Idempotency-Key": idempotencyKey },
    json: input,
  });
}

export type SupportIntakeReferenceKind = "checkout" | "payment" | "bank_transfer";

export type SupportIntake = {
  id: string;
  buyer_id?: string;
  reference_kind: SupportIntakeReferenceKind;
  reference: string;
  message: string;
  status: "open" | "linked" | "closed";
  linked_case_id?: string;
  handled_at?: string;
  close_reason?: string;
  version: number;
  created_at: string;
};

export function createSupportIntake(
  token: string,
  input: { reference_kind: SupportIntakeReferenceKind; reference: string; message: string },
  idempotencyKey: string,
): Promise<SupportIntake> {
  return request<SupportIntake>("/api/orders/support-intakes", {
    method: "POST",
    token,
    headers: { "Idempotency-Key": idempotencyKey },
    json: input,
  });
}

export function listMySupportIntakes(token: string): Promise<SupportIntake[]> {
  return request<SupportIntake[]>("/api/orders/support-intakes", { token });
}

export function listSupportIntakes(token: string, status = "open"): Promise<SupportIntake[]> {
  return request<SupportIntake[]>("/api/orders/admin/support-intakes", {
    token,
    query: { status },
  });
}

export function linkSupportIntake(
  token: string,
  intakeId: string,
  input: {
    order_id: string;
    vendor_order_id: string;
    category: SupportCategory;
    expected_version: number;
    reason: string;
  },
): Promise<SupportCase> {
  return request<SupportCase>(`/api/orders/admin/support-intakes/${intakeId}/links`, {
    method: "POST",
    token,
    json: input,
  });
}

export function closeSupportIntake(
  token: string,
  intakeId: string,
  input: { expected_version: number; reason: string },
): Promise<SupportIntake> {
  return request<SupportIntake>(`/api/orders/admin/support-intakes/${intakeId}/closure`, {
    method: "POST",
    token,
    json: input,
  });
}
