import type { Page, Route } from "@playwright/test";

// MockApi stands in for the gateway in browser tests. It keeps just enough
// server behaviour to check the frontend's contract: sessions per user,
// checkout idempotency by key, order ownership, payment status that only
// the "server" changes. Every value is fake.

export const API = "http://127.0.0.1:3999";
export const APP = "http://localhost:3100";

type Json = Record<string, unknown>;
export type MockUser = { id: string; email: string; full_name: string; role: string };

// Distinct in the first 8 characters too (the UI shows "#" + 8 characters).
const uuid = (n: number) =>
  `${String(n).padStart(8, "0")}-0000-4000-8000-${String(n).padStart(12, "0")}`;

export class MockApi {
  users = new Map<string, MockUser>();
  session: MockUser | null = null;
  cartItems: Json[] = [];
  cartVersion = 1;
  total = 150_000;
  orders = new Map<string, Json & { buyer_id: string; status: string }>();
  checkoutKeys = new Map<string, string>();
  checkoutRequests: { key: string | null; expected: unknown }[] = [];
  cancelCalls: string[] = [];
  unknownCalls: string[] = [];
  // Behaviour switches for one test.
  loseNextCheckoutResponse = false;
  emptyCartOnLostResponse = true;
  changeTotalOnce = false;
  products = new Map<string, Json>();
  reviews = new Map<string, Json[]>();
  private nextId = 100;

  constructor() {
    this.addUser({
      id: uuid(1),
      email: "buyer.a@example.test",
      full_name: "Buyer A",
      role: "buyer",
    });
    this.addUser({
      id: uuid(2),
      email: "buyer.b@example.test",
      full_name: "Buyer B",
      role: "buyer",
    });
    this.resetCart();
  }

  addUser(u: MockUser) {
    this.users.set(u.email, u);
    return u;
  }

  user(email: string) {
    return this.users.get(email)!;
  }

  resetCart() {
    this.cartItems = [
      {
        line_id: uuid(50),
        line_version: 1,
        product_id: uuid(60),
        product_name: "Áo thun thử nghiệm",
        quantity: 1,
        price_amount: 120_000,
        currency: "VND",
        price_changed: false,
        subtotal: 120_000,
        state: "available",
        stock_status: "in_stock",
        available: true,
      },
    ];
  }

  addOrder(buyer: MockUser, status = "pending_payment") {
    const id = uuid(this.nextId++);
    const order = {
      id,
      buyer_id: buyer.id,
      status,
      checkout_state: "ready",
      subtotal_amount: this.total - 30_000,
      shipping_amount: 30_000,
      total_amount: this.total,
      currency: "VND",
      recipient_name: "Người nhận thử",
      phone: "0900000000",
      province: "Hà Nội",
      district: "Ba Đình",
      ward: "Phúc Xá",
      street_address: "1 Đường Thử",
      items: [
        {
          id: uuid(this.nextId++),
          product_id: uuid(60),
          product_name: "Áo thun thử nghiệm",
          quantity: 1,
          price_amount: 120_000,
          subtotal_amount: 120_000,
        },
      ],
      vendor_orders: [],
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    this.orders.set(id, order);
    return order;
  }

  private cart() {
    return {
      version: this.cartVersion,
      items: this.cartItems,
      subtotal: this.cartItems.length ? { amount: 120_000, currency: "VND" } : null,
      total: this.cartItems.length ? 120_000 : 0,
      currency: "VND",
      mixed_currency: false,
      item_count: this.cartItems.length,
      line_count: this.cartItems.length,
      unavailable_lines: 0,
      price_changed_lines: 0,
      over_line_limit: false,
      checkout_ready: this.cartItems.length > 0,
      degraded: { catalog: false, inventory: false },
      limits: { max_lines: 100, max_quantity_per_line: 99 },
      page: { limit: 100, offset: 0, total: this.cartItems.length },
    };
  }

  private auth(u: MockUser) {
    return {
      user: { id: u.id, email: u.email, full_name: u.full_name, role: u.role },
      access_token: `token-${u.id}`,
      access_token_expires_at: new Date(Date.now() + 900_000).toISOString(),
      refresh_token_expires_at: new Date(Date.now() + 86_400_000).toISOString(),
    };
  }

  async install(page: Page) {
    await page.route(`${API}/**`, (route) => this.handle(route));
  }

  private send(route: Route, status: number, body: unknown) {
    return route.fulfill({
      status,
      contentType: "application/json",
      headers: {
        "Access-Control-Allow-Origin": APP,
        "Access-Control-Allow-Credentials": "true",
        "Access-Control-Allow-Headers": "*",
        "Access-Control-Allow-Methods": "GET, POST, PUT, PATCH, DELETE",
      },
      body: JSON.stringify(body),
    });
  }

  private ok(route: Route, data: unknown, status = 200) {
    return this.send(route, status, { data });
  }

  private fail(route: Route, status: number, code: string, message: string) {
    return this.send(route, status, { error: { code, message, request_id: "req-e2e-0001" } });
  }

  // caller is the user the Authorization header belongs to (as the real
  // services check the token, not anything the page claims).
  private caller(route: Route): MockUser | null {
    const header = route.request().headers()["authorization"] ?? "";
    const id = header.replace(/^Bearer token-/, "");
    return [...this.users.values()].find((u) => u.id === id) ?? null;
  }

  private async handle(route: Route) {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    const method = req.method();
    if (method === "OPTIONS") return this.send(route, 204, {});
    if (path === "/healthz") return this.send(route, 200, { status: "ok" });
    const body = (req.postDataJSON?.() ?? {}) as Json;

    if (path === "/api/auth/refresh" && method === "POST") {
      return this.session
        ? this.ok(route, this.auth(this.session))
        : this.fail(route, 401, "unauthorized", "Session expired");
    }
    if (path === "/api/auth/login" && method === "POST") {
      const u = this.users.get(String(body.email));
      if (!u || body.password !== "Fake-password-123") {
        return this.fail(route, 401, "invalid_credentials", "Email hoặc mật khẩu không đúng");
      }
      this.session = u;
      return this.ok(route, this.auth(u));
    }
    if (path === "/api/auth/logout" && method === "POST") {
      this.session = null;
      return this.ok(route, { logged_out: true });
    }

    // Public reads.
    if (method === "GET" && path.startsWith("/api/catalog/products/")) {
      const slug = decodeURIComponent(path.split("/").pop()!);
      const product = this.products.get(slug);
      return product ? this.ok(route, product) : this.fail(route, 404, "not_found", "Not found");
    }
    if (method === "GET" && /^\/api\/reviews\/products\/[^/]+$/.test(path)) {
      const reviews = this.reviews.get(path.split("/").pop()!) ?? [];
      return this.ok(route, {
        reviews,
        summary: {
          rating_average: reviews.length ? 5 : 0,
          rating_count: reviews.length,
          rating_distribution: [0, 0, 0, 0, reviews.length],
        },
      });
    }

    const me = this.caller(route);
    const signedIn = me !== null;
    if (path === "/api/cart" && method === "GET") {
      return signedIn
        ? this.ok(route, this.cart())
        : this.fail(route, 401, "unauthorized", "Sign in");
    }
    if (path === "/api/orders/addresses" && method === "GET") {
      return this.ok(route, [
        {
          id: uuid(70),
          recipient_name: "Người nhận thử",
          phone: "0900000000",
          province: "Hà Nội",
          district: "Ba Đình",
          ward: "Phúc Xá",
          street_address: "1 Đường Thử",
          is_default: true,
        },
      ]);
    }
    if (path === "/api/orders/checkout/preview" && method === "POST") {
      return this.ok(route, {
        cart_version: this.cartVersion,
        currency: "VND",
        subtotal_amount: this.total - 30_000,
        shipping_amount: 30_000,
        total_amount: this.total,
        ready: true,
        vendors: [
          {
            vendor_id: uuid(80),
            subtotal_amount: this.total - 30_000,
            shipping_fee_amount: 30_000,
            item_count: 1,
          },
        ],
      });
    }
    if (path === "/api/orders/checkout" && method === "POST" && me) {
      const key = req.headers()["idempotency-key"] ?? null;
      this.checkoutRequests.push({ key, expected: body.expected_total_amount });
      if (key && this.checkoutKeys.has(key)) {
        return this.ok(route, this.orders.get(this.checkoutKeys.get(key)!), 200);
      }
      if (this.changeTotalOnce) {
        this.changeTotalOnce = false;
        this.total += 15_000;
        return this.fail(route, 409, "checkout_total_changed", "Total changed");
      }
      if (body.expected_total_amount !== this.total) {
        return this.fail(route, 409, "checkout_total_changed", "Total changed");
      }
      const order = this.addOrder(me);
      if (key) this.checkoutKeys.set(key, order.id);
      if (this.loseNextCheckoutResponse) {
        this.loseNextCheckoutResponse = false;
        if (this.emptyCartOnLostResponse) {
          this.cartItems = [];
          this.cartVersion++;
        }
        return route.abort("failed");
      }
      this.cartItems = [];
      this.cartVersion++;
      return this.ok(route, order, 201);
    }
    if (path === "/api/orders/mine" && method === "GET" && me) {
      return this.ok(
        route,
        [...this.orders.values()].filter((o) => o.buyer_id === me.id),
      );
    }
    const orderMatch = path.match(/^\/api\/orders\/([0-9a-f-]{36})(\/cancel)?$/);
    if (orderMatch && me) {
      const order = this.orders.get(orderMatch[1] ?? "");
      if (!order || order.buyer_id !== me.id) {
        return this.fail(route, 404, "not_found", "Không tìm thấy đơn hàng");
      }
      if (orderMatch[2] && method === "POST") {
        this.cancelCalls.push(String(order.id));
        order.status = "cancelled";
      }
      return this.ok(route, order);
    }
    if (
      method === "GET" &&
      (path.startsWith("/api/catalog/") ||
        path.startsWith("/api/shipments/") ||
        path.startsWith("/api/reviews/"))
    ) {
      return this.ok(route, []);
    }
    this.unknownCalls.push(`${method} ${path}`);
    return signedIn
      ? this.fail(route, 404, "not_found", "Not found in mock")
      : this.fail(route, 401, "unauthorized", "Sign in");
  }
}
