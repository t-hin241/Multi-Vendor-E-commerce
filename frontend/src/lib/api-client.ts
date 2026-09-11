// Thin typed client for calling the API Gateway. All backend endpoints are
// public through the gateway, never called directly service-to-service from
// the browser.
const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

type ErrorEnvelope = { error: { code: string; message: string; request_id?: string } };
type SuccessEnvelope<T> = { data: T };

type RequestOptions = {
  method?: string;
  token?: string;
  json?: unknown;
  form?: FormData;
  query?: Record<string, string | number | undefined>;
};

function buildQuery(query?: Record<string, string | number | undefined>): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", token, json, form, query } = options;

  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (json !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(`${API_BASE_URL}${path}${buildQuery(query)}`, {
    method,
    headers,
    body: form ?? (json !== undefined ? JSON.stringify(json) : undefined),
    cache: "no-store",
  });

  const body = (await res.json().catch(() => null)) as SuccessEnvelope<T> | ErrorEnvelope | null;

  if (!res.ok || !body || "error" in body) {
    const errBody = body && "error" in body ? body.error : null;
    throw new ApiError(
      res.status,
      errBody?.code ?? "unknown_error",
      errBody?.message ?? `Request failed with status ${res.status}`,
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
  refresh_token: string;
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

export function refreshSession(refreshToken: string): Promise<AuthResult> {
  return request<AuthResult>("/api/auth/refresh", {
    method: "POST",
    json: { refresh_token: refreshToken },
  });
}

export function logout(refreshToken: string): Promise<{ logged_out: boolean }> {
  return request("/api/auth/logout", { method: "POST", json: { refresh_token: refreshToken } });
}

// ---------- Catalog ----------

export type Category = {
  id: string;
  name: string;
  slug: string;
  parent_id: string | null;
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

export type ProductStatus = "pending_review" | "approved" | "rejected";

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
    json: { code, name, data_type: dataType, unit: unit ?? null, is_variant_defining: isVariantDefining ?? false },
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
  params: { limit?: number; offset?: number } = {},
): Promise<InventoryItem[]> {
  return request<InventoryItem[]>("/api/inventory/items/mine", { token, query: params });
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

export function restockProduct(token: string, productId: string, quantity: number): Promise<InventoryItem> {
  return request<InventoryItem>(`/api/inventory/items/${productId}/restock`, {
    method: "PATCH",
    token,
    json: { quantity },
  });
}

export function restockVariant(token: string, variantId: string, quantity: number): Promise<InventoryItem> {
  return request<InventoryItem>(`/api/inventory/items/variant/${variantId}/restock`, {
    method: "PATCH",
    token,
    json: { quantity },
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
  params: { categoryId?: string; q?: string; limit?: number; offset?: number } = {},
): Promise<StorefrontListing> {
  return request<StorefrontListing>("/api/catalog/products", {
    query: {
      category_id: params.categoryId,
      q: params.q,
      limit: params.limit,
      offset: params.offset,
    },
  });
}

export function getProductBySlug(slug: string): Promise<Product> {
  return request<Product>(`/api/catalog/products/${encodeURIComponent(slug)}`);
}

export function createProduct(
  token: string,
  input: {
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
  params: { limit?: number; offset?: number } = {},
): Promise<Product[]> {
  return request<Product[]>("/api/catalog/products/mine", { token, query: params });
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

// ---------- Vendor ----------

export type VendorStatus = "pending" | "approved" | "rejected";

export type Vendor = {
  id: string;
  user_id: string;
  shop_name: string;
  description: string;
  status: VendorStatus;
  rejection_reason?: string;
  approved_at?: string;
  created_at: string;
  updated_at: string;
};

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

export function getMyVendor(token: string): Promise<Vendor> {
  return request<Vendor>("/api/vendor/me", { token });
}

export function updateVendorProfile(
  token: string,
  shopName: string,
  description: string,
): Promise<Vendor> {
  return request<Vendor>("/api/vendor/me", {
    method: "PATCH",
    token,
    json: { shop_name: shopName, description },
  });
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

// ---------- Cart ----------

export type CartLine = {
  product_id: string;
  product_name?: string;
  variant_id?: string;
  variant_sku?: string;
  variant_label?: string;
  quantity: number;
  price_amount: number;
  currency?: string;
  subtotal: number;
  available: boolean;
};

export type Cart = { items: CartLine[]; total: number };

export function getCart(token: string): Promise<Cart> {
  return request<Cart>("/api/cart", { token });
}

export function addCartItem(
  token: string,
  productId: string,
  quantity: number,
  variantId?: string,
): Promise<Cart> {
  return request<Cart>("/api/cart/items", {
    method: "POST",
    token,
    json: { product_id: productId, variant_id: variantId ?? null, quantity },
  });
}

export function setCartItemQuantity(
  token: string,
  productId: string,
  quantity: number,
  variantId?: string,
): Promise<Cart> {
  return request<Cart>(`/api/cart/items/${productId}`, {
    method: "PATCH",
    token,
    json: { quantity },
    query: { variant_id: variantId },
  });
}

export function removeCartItem(
  token: string,
  productId: string,
  variantId?: string,
): Promise<{ removed: boolean }> {
  return request(`/api/cart/items/${productId}`, { method: "DELETE", token, query: { variant_id: variantId } });
}

export function clearCart(token: string): Promise<{ cleared: boolean }> {
  return request("/api/cart", { method: "DELETE", token });
}

// ---------- Orders ----------

export type OrderStatus =
  "pending_payment" | "paid" | "processing" | "shipped" | "completed" | "cancelled" | "refunded";

export type OrderItem = {
  product_id: string;
  product_name: string;
  variant_id?: string;
  variant_sku?: string;
  variant_label?: string;
  price_amount: number;
  quantity: number;
  subtotal_amount: number;
};

export type VendorOrder = {
  id: string;
  order_id: string;
  status: OrderStatus;
  subtotal_amount: number;
  shipping_fee_amount: number;
  currency: string;
  commission_rate_bps?: number;
  commission_amount?: number;
  net_amount?: number;
  items?: OrderItem[];
  created_at: string;
};

export type Order = {
  id: string;
  status: OrderStatus;
  total_amount: number;
  currency: string;
  cancellation_reason?: string;
  recipient_name: string;
  phone: string;
  province: string;
  district: string;
  ward: string;
  street_address: string;
  items?: OrderItem[];
  vendor_orders?: VendorOrder[];
  created_at: string;
  updated_at: string;
};

export function checkout(token: string, addressId: string): Promise<Order> {
  return request<Order>("/api/orders/checkout", {
    method: "POST",
    token,
    json: { address_id: addressId },
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

export function deleteBuyerAddress(token: string, addressId: string): Promise<{ deleted: boolean }> {
  return request(`/api/orders/addresses/${addressId}`, { method: "DELETE", token });
}

export function setDefaultBuyerAddress(token: string, addressId: string): Promise<{ updated: boolean }> {
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
  params: { limit?: number; offset?: number } = {},
): Promise<VendorOrder[]> {
  return request<VendorOrder[]>("/api/orders/vendor/mine", { token, query: params });
}

export function listAdminOrders(
  token: string,
  params: { status?: string; limit?: number; offset?: number } = {},
): Promise<Order[]> {
  return request<Order[]>("/api/orders/admin", { token, query: params });
}

export function adminTransitionOrder(
  token: string,
  orderId: string,
  status: "cancelled" | "refunded",
  reason: string,
): Promise<Order> {
  return request<Order>(`/api/orders/admin/${orderId}/transition`, {
    method: "POST",
    token,
    json: { status, reason },
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
  top_products: TopProduct[];
};

export function getVendorSummary(token: string): Promise<VendorSummary> {
  return request<VendorSummary>("/api/orders/vendor/summary", { token });
}

// exportVendorOrdersCSV fetches the raw CSV body directly (not through
// request(), which expects the {data: ...} JSON envelope every other
// endpoint uses) so the caller can hand it to the browser as a file download.
export async function exportVendorOrdersCSV(token: string): Promise<string> {
  const res = await fetch(`${API_BASE_URL}/api/orders/vendor/export.csv`, {
    headers: { Authorization: `Bearer ${token}` },
    cache: "no-store",
  });
  if (!res.ok) {
    throw new ApiError(res.status, "export_failed", "Could not export orders");
  }
  return res.text();
}

export type CommissionRule = {
  id: string;
  rate_bps: number;
  created_by?: string;
  created_at: string;
};

export function listCommissionRules(token: string): Promise<CommissionRule[]> {
  return request<CommissionRule[]>("/api/orders/admin/commission-rules", { token });
}

export function setCommissionRule(token: string, rateBps: number): Promise<CommissionRule> {
  return request<CommissionRule>("/api/orders/admin/commission-rules", {
    method: "POST",
    token,
    json: { rate_bps: rateBps },
  });
}

// ---------- Payments ----------

export type PaymentStatus = "pending" | "authorized" | "captured" | "failed" | "refunded";

export type PaymentIntent = {
  id: string;
  order_id: string;
  amount: number;
  currency: string;
  status: PaymentStatus;
  provider: string;
  provider_intent_id: string;
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

// ---------- Shipments ----------

export type ShipmentStatus =
  | "pending"
  | "ready_to_ship"
  | "shipped"
  | "delivered"
  | "cancelled"
  | "interception_requested";

export type Shipment = {
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

export function listMyShipments(
  token: string,
  params: { limit?: number; offset?: number } = {},
): Promise<Shipment[]> {
  return request<Shipment[]>("/api/shipments/mine", { token, query: params });
}

export type TrackingEvent = {
  status: ShipmentStatus;
  note?: string;
  created_at: string;
};

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

export function enableShippingMethod(token: string, carrierId: string): Promise<VendorShippingMethod> {
  return request<VendorShippingMethod>("/api/shipments/vendor/methods", {
    method: "POST",
    token,
    json: { carrier_id: carrierId },
  });
}

export function listMyShippingMethods(token: string): Promise<VendorShippingMethod[]> {
  return request<VendorShippingMethod[]>("/api/shipments/vendor/methods", { token });
}

export function setDefaultShippingMethod(token: string, methodId: string): Promise<{ updated: boolean }> {
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

export function addVendorAddress(token: string, input: AddressInput): Promise<VendorAddress> {
  return request<VendorAddress>("/api/vendor/addresses", { method: "POST", token, json: input });
}

export function listVendorAddresses(token: string): Promise<VendorAddress[]> {
  return request<VendorAddress[]>("/api/vendor/addresses", { token });
}

export function updateVendorAddress(
  token: string,
  addressId: string,
  input: AddressInput,
): Promise<VendorAddress> {
  return request<VendorAddress>(`/api/vendor/addresses/${addressId}`, {
    method: "PATCH",
    token,
    json: input,
  });
}

export function deleteVendorAddress(token: string, addressId: string): Promise<{ deleted: boolean }> {
  return request(`/api/vendor/addresses/${addressId}`, { method: "DELETE", token });
}

export function setDefaultVendorAddress(token: string, addressId: string): Promise<{ updated: boolean }> {
  return request(`/api/vendor/addresses/${addressId}/default`, { method: "PATCH", token });
}

// ---------- Admin: carriers, zones, fee rules ----------

export type Carrier = { id: string; name: string; code: string; is_active: boolean };

export function createCarrier(token: string, name: string, code: string): Promise<Carrier> {
  return request<Carrier>("/api/shipments/admin/carriers", {
    method: "POST",
    token,
    json: { name, code },
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

export function setCarrierActive(token: string, carrierId: string, isActive: boolean): Promise<{ updated: boolean }> {
  return request(`/api/shipments/admin/carriers/${carrierId}/active`, {
    method: "PATCH",
    token,
    json: { is_active: isActive },
  });
}

export type ShippingZone = { id: string; name: string; code: string };

export function createZone(token: string, name: string, code: string): Promise<ShippingZone> {
  return request<ShippingZone>("/api/shipments/admin/zones", {
    method: "POST",
    token,
    json: { name, code },
  });
}

export function listZones(token: string): Promise<ShippingZone[]> {
  return request<ShippingZone[]>("/api/shipments/admin/zones", { token });
}

export function addProvinceToZone(
  token: string,
  zoneId: string,
  provinceCode: string,
): Promise<{ added: boolean }> {
  return request(`/api/shipments/admin/zones/${zoneId}/provinces`, {
    method: "POST",
    token,
    json: { province_code: provinceCode },
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
    },
  });
}

export function listFeeRules(token: string): Promise<FeeRule[]> {
  return request<FeeRule[]>("/api/shipments/admin/fee-rules", { token });
}
