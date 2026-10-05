// Shared helpers for the load-test scenarios (deploy/loadtest/README.md).
// Everything goes through the gateway like browser traffic: same headers,
// same rate limits. Each virtual user is one client with its own address
// (the gateway trusts k6's X-Forwarded-For in the load-test environment).
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

export const BASE = __ENV.BASE_URL || 'http://gateway:8080';
const ORIGIN = __ENV.ORIGIN || 'http://localhost:3000';
const RUN = __ENV.RUN_ID || 'run';
// Shipping is configured for this province in the test data (run.sh fixture).
const PROVINCE = __ENV.PROVINCE || 'Hà Nội';

// A virtual user buys far more often than a person (several purchases a
// minute), which would trip the gateway's per-address checkout limit
// (20/min) and measure the limiter. Each iteration takes one of 64
// addresses of its user, so every address stays at a person's pace.
export function clientIP() {
  let iteration = 0;
  try {
    iteration = exec.vu.iterationInScenario; // not available in setup()
  } catch (e) {
    iteration = 0;
  }
  const n = exec.vu.idInTest * 64 + (iteration % 64);
  return `10.${(n >> 16) & 255}.${(n >> 8) & 255}.${n & 255}`;
}

function headers(token, extra) {
  const h = { 'Content-Type': 'application/json', 'X-Forwarded-For': clientIP(), Origin: ORIGIN, 'X-CSRF-Protection': '1' };
  if (token) h.Authorization = `Bearer ${token}`;
  return Object.assign(h, extra || {});
}

// name tags the request so results group by operation, not by URL.
export function get(path, name, token) {
  return http.get(BASE + path, { headers: headers(token), tags: { name } });
}
export function send(method, path, body, name, token, extra) {
  return http.request(method, BASE + path, body === undefined ? null : JSON.stringify(body), { headers: headers(token, extra), tags: { name } });
}
export function data(res) {
  try {
    return res.json('data');
  } catch (e) {
    return null;
  }
}
export function ok(res, name) {
  const passed = check(res, { [`${name} 2xx`]: (r) => r.status >= 200 && r.status < 300 });
  // DEBUG=1 prints failed answers (error envelopes: code, message, request id).
  if (!passed && __ENV.DEBUG === "1") console.warn(`${name}: ${res.status} ${String(res.body).slice(0, 300)}`);
  return passed;
}
export function pick(list) {
  return list[Math.floor(Math.random() * list.length)];
}

// One buyer per virtual user, registered on first use and logged in again
// before its access token gets old.
let session = null;
export function buyer() {
  if (session && Date.now() - session.at < 8 * 60 * 1000) return session;
  const email = `lt-${RUN}-${exec.vu.idInTest}@loadtest.invalid`;
  const password = 'loadtest-password-not-a-secret';
  let res = session ? null : send('POST', '/api/auth/register', { email, password, full_name: 'Load Test Buyer', role: 'buyer' }, 'auth register');
  if (!res || res.status >= 300) res = send('POST', '/api/auth/login', { email, password }, 'auth login');
  if (!ok(res, 'auth')) throw new Error(`buyer login failed: ${res.status} ${res.body}`);
  const token = data(res).access_token;
  let addressId = session && session.addressId;
  if (!addressId) {
    const addr = send('POST', '/api/orders/addresses', {
      recipient_name: 'Load Test', phone: '0900000000', province: PROVINCE,
      district: 'Ba Đình', ward: 'Phúc Xá', street_address: '1 Load Test',
    }, 'address add', token);
    if (!ok(addr, 'address add')) throw new Error(`address failed: ${addr.status} ${addr.body}`);
    addressId = data(addr).id;
  }
  session = { token, addressId, at: Date.now() };
  return session;
}

// Purchasable items for the run: listing pages from several offsets, then
// each product's detail for its variants and stock. Runs once in setup().
export function catalogSample(products = Number(__ENV.SAMPLE_PRODUCTS || 300)) {
  const items = [];
  const listing = [];
  for (let offset = 0; listing.length < products && offset <= 4000; offset += 20) {
    const res = get(`/api/catalog/products?limit=20&offset=${offset}`, "setup listing");
    const page = data(res);
    if (!page || !page.products || page.products.length === 0) break;
    listing.push(...page.products);
  }
  for (const p of listing.slice(0, products)) {
    const detail = data(get(`/api/catalog/products/${p.slug}`, 'setup detail'));
    if (!detail) continue;
    if (detail.variants && detail.variants.length > 0) {
      const v = detail.variants.find((x) => (x.available_quantity || 0) > 5);
      if (v) items.push({ product: p.id, variant: v.id, vendor: p.vendor_id, slug: p.slug });
    } else if ((detail.stock_quantity === undefined || detail.stock_quantity > 5)) {
      items.push({ product: p.id, variant: null, vendor: p.vendor_id, slug: p.slug });
    }
  }
  const slugs = listing.map((p) => p.slug);
  if (items.length < 20) throw new Error(`only ${items.length} purchasable items found`);
  return { items, slugs };
}

// Items from `vendors` different shops, `lines` cart lines in total.
export function basket(items, lines, vendors) {
  const byVendor = {};
  for (const it of items) (byVendor[it.vendor] = byVendor[it.vendor] || []).push(it);
  const shops = Object.keys(byVendor).sort(() => Math.random() - 0.5).slice(0, vendors);
  const chosen = [];
  for (let i = 0; chosen.length < lines && i < lines * 10; i++) {
    const it = pick(byVendor[shops[i % shops.length]]);
    if (!chosen.some((c) => c.product === it.product)) chosen.push(it);
  }
  return chosen;
}

// Open-model load: iterations per second rising in steps, so the point
// where latency or errors break (the knee) shows against a known rate.
export function steps(scale = 1) {
  const start = Number(__ENV.START_RATE || 2) * scale;
  const step = Number(__ENV.STEP_RATE || 2) * scale;
  const count = Number(__ENV.STEPS || 6);
  const hold = __ENV.STEP_DURATION || '2m';
  const stages = [];
  for (let i = 0; i < count; i++) {
    const rate = Math.max(1, Math.round(start + i * step));
    stages.push({ target: rate, duration: '20s' }, { target: rate, duration: hold });
  }
  return stages;
}
