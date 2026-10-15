// AF-10 rollout rehearsal journeys (PW-049). Run through
// deploy/rehearsal/variant-pricing.sh e2e <phase>; every call goes through the
// gateway like browser traffic. Accounts are synthetic (@rehearsal.invalid),
// passwords are generated per run and kept only in the state file, outside
// the repository. Each phase records its checks in the state file so the
// release record can cite them.
import { spawnSync } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { crc32, deflateSync } from 'node:zlib';

const BASE = process.env.REHEARSAL_BASE_URL;
const ORIGIN = process.env.REHEARSAL_ORIGIN;
const STATE_FILE = process.env.REHEARSAL_STATE_FILE;
const SCRIPT = process.env.REHEARSAL_SCRIPT;
const PROVINCE = process.env.REHEARSAL_PROVINCE || 'Hà Nội';
if (!BASE || !ORIGIN || !STATE_FILE || !SCRIPT) throw new Error('run through deploy/rehearsal/variant-pricing.sh e2e <phase>');

const state = existsSync(STATE_FILE) ? JSON.parse(readFileSync(STATE_FILE, 'utf8')) : { run: randomBytes(3).toString('hex'), checks: [] };
const save = () => writeFileSync(STATE_FILE, JSON.stringify(state, null, 1));
let phase = '';

function check(name, ok, detail = {}) {
  state.checks.push({ phase, name, ok: Boolean(ok), at: new Date().toISOString(), ...detail });
  save();
  console.log(`${ok ? 'PASS' : 'FAIL'} [${phase}] ${name}${Object.keys(detail).length ? ' ' + JSON.stringify(detail) : ''}`);
  if (!ok) {
    process.exitCode = 1;
    throw new Error(`check failed: ${name}`);
  }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(method, path, { token, body, headers = {} } = {}) {
  const h = { 'Content-Type': 'application/json', Origin: ORIGIN, 'X-CSRF-Protection': '1', ...headers };
  if (token) h.Authorization = `Bearer ${token}`;
  const res = await fetch(BASE + path, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    json = null;
  }
  return { status: res.status, json, data: json && json.data, error: json && json.error, text };
}
// A call that must succeed; anything else stops the phase with the answer.
async function must(method, path, opts) {
  const r = await api(method, path, opts);
  if (r.status < 200 || r.status > 299) throw new Error(`${method} ${path} -> ${r.status} ${r.text.slice(0, 400)}`);
  return r.data;
}
function errorCode(r) {
  return (r.error && (r.error.code || r.error)) || (r.json && r.json.code) || '';
}

// SQL on the rehearsal copy, through the orchestration script: reads, and
// the one admin fixture below.
function sql(db, query) {
  const out = spawnSync('bash', [SCRIPT, 'sql', db, '-Atc', query], { encoding: 'utf8' });
  if (out.status !== 0) throw new Error(`sql ${db}: ${out.stderr}`);
  return out.stdout.trim();
}
function nats(action) {
  const out = spawnSync('bash', [SCRIPT, 'nats', action], { encoding: 'utf8' });
  if (out.status !== 0) throw new Error(`nats ${action}: ${out.stderr}`);
}

async function login(email, password) {
  return (await must('POST', '/api/auth/login', { body: { email, password } })).access_token;
}
async function account(key, role) {
  state.accounts = state.accounts || {};
  let a = state.accounts[key];
  if (!a) {
    a = { email: `pw049-${key}-${state.run}-${randomBytes(2).toString('hex')}@rehearsal.invalid`, password: `rh-${randomBytes(12).toString('hex')}` };
    state.accounts[key] = a;
    save();
  }
  if (!a.registered) {
    // Kept before the call: a commit that outlives the request timeout
    // still creates the account (409 on the retry).
    const r = await api('POST', '/api/auth/register', { body: { email: a.email, password: a.password, full_name: `PW049 ${key}`, role } });
    if (r.status >= 300 && r.status !== 409) throw new Error(`register ${key}: ${r.status} ${r.text.slice(0, 300)}`);
    a.registered = true;
    save();
  }
  return a;
}
async function admin() {
  const a = await account('admin', 'buyer');
  // Rehearsal copy only: the copied cluster already has admins, so the
  // bootstrap command refuses; promote this synthetic account instead.
  sql('identity_db', `UPDATE users SET role='admin' WHERE email='${a.email}' AND role<>'admin'`);
  return login(a.email, a.password);
}
async function buyerAddress(token) {
  if (state.addressId) return state.addressId;
  const addr = await must('POST', '/api/orders/addresses', {
    token,
    body: { recipient_name: 'PW049 Buyer', phone: '0900000049', province: PROVINCE, district: 'Ba Đình', ward: 'Phúc Xá', street_address: '49 Rehearsal' },
  });
  state.addressId = addr.id;
  save();
  return addr.id;
}

async function productDetail(slugOrId) {
  return must('GET', `/api/catalog/products/${slugOrId}`);
}
async function cart(token) {
  return must('GET', '/api/cart', { token });
}
async function clearCart(token) {
  await api('DELETE', '/api/cart', { token });
}
async function addToCart(token, productId, variantId, quantity = 1) {
  const body = { product_id: productId, quantity };
  if (variantId) body.variant_id = variantId;
  return api('POST', '/api/cart/items', { token, body });
}
async function confirmPrices(token) {
  const c = await cart(token);
  const lines = c.items.filter((l) => l.price_changed || l.seen_pricing_version == null)
    // The price the buyer was shown; Cart refuses it if Catalog moved on.
    .map((l) => ({ line_id: l.line_id, price_amount: l.price_amount, currency: l.currency, variant_id: l.variant_id || undefined, pricing_version: l.pricing_version }));
  if (lines.length === 0) return c;
  await must('POST', '/api/cart/price-confirmations', { token, body: { expected_version: c.version, lines } });
  return cart(token);
}
async function preview(token) {
  return api('POST', '/api/orders/checkout/preview', { token, body: { address_id: state.addressId } });
}
async function checkout(token, cartVersion, quote, extra = {}) {
  const body = { address_id: state.addressId, cart_version: cartVersion, ...extra };
  if (quote) body.expected_quote_version = quote.quote_version;
  if (quote && quote.policy_versions) body.accepted_policy_versions = quote.policy_versions;
  return api('POST', '/api/orders/checkout', { token, body, headers: { 'Idempotency-Key': `pw049-${randomUUID()}` } });
}
async function pay(token, orderId) {
  const intent = await must('POST', '/api/payments/intents', { token, body: { order_id: orderId } });
  await must('POST', `/api/payments/intents/${intent.id}/simulate`, { token, body: { outcome: 'succeeded' } });
  for (let i = 0; i < 30; i++) {
    const o = await must('GET', `/api/orders/${orderId}`, { token });
    if (o.status === 'paid') return { intent, order: o };
    await sleep(1000);
  }
  throw new Error(`order ${orderId} did not become paid`);
}
// Cart -> preview -> checkout -> capture for whatever is in the cart.
async function purchase(token) {
  const c = await confirmPrices(token);
  if (!c.checkout_ready) throw new Error(`cart not ready: ${JSON.stringify(c.items.map((l) => [l.state, l.price_changed, l.available]))}`);
  const p = await preview(token);
  if (p.status !== 200 || !p.data.ready) {
    const { lines, policies, ...rest } = (p.data || {});
    throw new Error(`preview: ${p.status} ${JSON.stringify(p.data ? { ...rest, lines: (lines || []).map((l) => [l.status, l.is_sellable, l.unit_price, l.pricing_source]) } : p.text).slice(0, 900)}`);
  }
  const o = await checkout(token, c.version, p.data);
  if (o.status !== 201 && o.status !== 200) throw new Error(`checkout: ${o.status} ${o.text.slice(0, 400)}`);
  const orderId = o.data.id || o.data.order_id;
  const paid = await pay(token, orderId);
  return { cart: c, preview: p.data, orderId, ...paid };
}

async function approveProduct(adminToken, vendorToken, productId) {
  const p = await must('GET', `/api/catalog/products/${productId}/editor`, { token: vendorToken });
  if (p.product.status !== 'pending' && p.product.status !== 'approved') {
    await must('PATCH', `/api/catalog/products/${productId}/submit`, { token: vendorToken });
  }
  if (p.product.status !== 'approved') await must('PATCH', `/api/catalog/products/admin/${productId}/approve`, { token: adminToken });
  // Sellable once the status fence is enforced (selling_sync_pending=false),
  // which follows the storefront by the status outbox round trip.
  for (let i = 0; i < 60; i++) {
    const r = await api('GET', `/api/catalog/products/${p.product.slug}`);
    if (r.status === 200 && !r.data.selling_sync_pending) return r.data;
    await sleep(1000);
  }
  throw new Error(`product ${productId} not sellable on the storefront`);
}

// Pilot shop on the base release: application, approval, pickup address,
// test carrier. Admin approves through the API like an operator.
async function pilotShop(adminToken) {
  const v = await account('vendor', 'vendor');
  const token = await login(v.email, v.password);
  state.shop = state.shop || {};
  if (!state.vendorId) {
    const shop = await must('POST', '/api/vendor/applications', { token, body: { shop_name: `PW049 Pilot ${state.run}`, description: 'Rehearsal pilot shop (synthetic)' } });
    state.vendorId = shop.id;
    save();
  }
  if (!state.shop.address) {
    // A default pickup address comes before approval.
    await must('POST', `/api/vendor/${state.vendorId}/addresses`, {
      token,
      body: { recipient_name: 'PW049 Shop', phone: '0900000050', province: PROVINCE, district: 'Ba Đình', ward: 'Phúc Xá', street_address: '50 Rehearsal' },
    });
    state.shop.address = true;
    save();
  }
  if (!state.shop.approved) {
    await must('PATCH', `/api/vendor/admin/applications/${state.vendorId}/approve`, { token: adminToken });
    state.shop.approved = true;
    save();
  }
  if (!state.shop.shipping) {
    const carriers = await must('GET', '/api/shipments/carriers');
    const carrier = (carriers.carriers || carriers).find((c) => c.code === 'TEST-EXP');
    if (!carrier) throw new Error('no TEST-EXP carrier');
    await must('POST', '/api/shipments/vendor/methods', { token, body: { vendor_id: state.vendorId, carrier_id: carrier.id } });
    state.shop.shipping = true;
    save();
  }
  return token;
}

function attributeValues(template) {
  const out = [];
  for (const f of template.attributes || []) {
    if (f.is_variant_defining) continue;
    if (!f.required && !f.code.startsWith('pkg_')) continue;
    if (f.data_type === 'select' || f.data_type === 'multi_select') out.push({ attribute_id: f.attribute_id, option_ids: [f.options[0].id] });
    else if (f.data_type === 'number') out.push({ attribute_id: f.attribute_id, value: f.code === 'pkg_weight' ? '500' : '10' });
    else if (f.data_type === 'boolean') out.push({ attribute_id: f.attribute_id, value: 'true' });
    else out.push({ attribute_id: f.attribute_id, value: 'PW-049 rehearsal' });
  }
  return out;
}
// A leaf category whose template has a variant-defining attribute with two
// options, picked from the copied data.
async function variantCategory() {
  if (state.category) return state.category;
  const ids = sql('catalog_db', `SELECT p.category_id FROM products p JOIN product_variants v ON v.product_id=p.id
    GROUP BY p.category_id ORDER BY count(DISTINCT p.id) DESC LIMIT 20`).split('\n');
  for (const id of ids) {
    const t = await must('GET', `/api/catalog/categories/${id}/attribute-template`);
    // Every variant-defining attribute needs an option per SKU: the first
    // one tells the two SKUs apart, the others stay on their first option.
    const axes = (t.attributes || []).filter((f) => f.is_variant_defining && (f.options || []).length >= 1);
    if (axes.length > 0 && axes[0].options.length >= 2) {
      state.category = { id, axes: axes.map((a) => a.options.map((o) => o.id)), template: t };
      save();
      return state.category;
    }
  }
  throw new Error('no category with a two-option variant attribute');
}
// A category whose products are sold without SKUs.
async function plainCategory() {
  if (state.plainCategory) return state.plainCategory;
  const ids = sql('catalog_db', `SELECT p.category_id FROM products p WHERE NOT EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id=p.id)
    GROUP BY p.category_id ORDER BY count(*) DESC LIMIT 20`).split('\n');
  for (const id of ids) {
    const t = await must('GET', `/api/catalog/categories/${id}/attribute-template`);
    if (!(t.attributes || []).some((f) => f.is_variant_defining)) {
      state.plainCategory = { id, template: t };
      save();
      return state.plainCategory;
    }
  }
  throw new Error('no category without variant attributes');
}
// Shipping is priced by package weight, which comes from pkg_* attributes.
// The copied categories have none bound, and a content edit rewrites
// packaging from attributes, so the operator binds pkg_weight (optional)
// to the pilot categories, as staging would configure it.
async function ensurePackagingRule(adminToken, cat) {
  if ((cat.template.attributes || []).some((f) => f.code === 'pkg_weight')) return;
  const attrs = await must('GET', '/api/catalog/attributes?limit=200', { token: adminToken });
  const weight = (attrs.attributes || attrs.items || attrs).find((a) => a.code === 'pkg_weight');
  if (!weight) throw new Error('no pkg_weight attribute');
  await must('POST', `/api/catalog/categories/${cat.id}/attribute-rules`, { token: adminToken, body: { attribute_id: weight.id, is_required: false, is_excluded: false, position: 90 } });
  cat.template = await must('GET', `/api/catalog/categories/${cat.id}/attribute-template`);
  save();
}
// A 64x64 solid PNG: products need one image before review.
function png() {
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body));
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(64, 0);
  ihdr.writeUInt32BE(64, 4);
  ihdr.set([8, 2, 0, 0, 0], 8);
  const row = Buffer.concat([Buffer.from([0]), Buffer.alloc(64 * 3, 0x7a)]);
  const pixels = Buffer.concat(Array.from({ length: 64 }, () => row));
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(pixels)), chunk('IEND', Buffer.alloc(0))]);
}
async function uploadImage(token, productId) {
  const form = new FormData();
  form.append('image', new Blob([png()], { type: 'image/png' }), 'pw049.png');
  const res = await fetch(`${BASE}/api/catalog/products/${productId}/images`, {
    method: 'POST', body: form, headers: { Authorization: `Bearer ${token}`, Origin: ORIGIN, 'X-CSRF-Protection': '1' },
  });
  if (!res.ok) throw new Error(`image upload ${res.status} ${(await res.text()).slice(0, 300)}`);
}
async function createProduct(adminToken, vendorToken, name, price, skus) {
  const cat = skus > 0 ? await variantCategory() : await plainCategory();
  await ensurePackagingRule(adminToken, cat);
  const p = await must('POST', '/api/catalog/products', {
    token: vendorToken,
    body: { vendor_id: state.vendorId, category_id: cat.id, name, description: 'Synthetic rehearsal product', price_amount: price, attributes: attributeValues(cat.template) },
  });
  await uploadImage(vendorToken, p.id);
  const variants = [];
  for (let i = 0; i < skus; i++) {
    const v = await must('POST', `/api/catalog/products/${p.id}/variants`, { token: vendorToken, body: { sku: `PW049-${state.run}-${p.id.slice(0, 6)}-${i}`, option_ids: cat.axes.map((opts, k) => opts[k === 0 ? i : 0]) } });
    variants.push(v.id);
    await must('POST', '/api/inventory/items', { token: vendorToken, body: { variant_id: v.id, initial_quantity: 50 } });
  }
  if (skus === 0) await must('POST', '/api/inventory/items', { token: vendorToken, body: { product_id: p.id, initial_quantity: 50 } });
  const weight = sql('catalog_db', `SELECT coalesce(weight_grams,0) FROM product_packaging WHERE product_id='${p.id}'`);
  if (!(Number(weight) > 0)) throw new Error(`product ${p.id} has no package weight`);
  return { id: p.id, slug: p.slug, variants };
}
async function vendorOrderOf(buyerToken, orderId) {
  const o = await must('GET', `/api/orders/${orderId}`, { token: buyerToken });
  const vo = (o.vendor_orders || [])[0];
  return { order: o, vendorOrderId: vo && vo.id, items: (o.vendor_orders || []).flatMap((x) => x.items || []).concat(o.items || []) };
}
async function deliver(vendorToken, vendorOrderId) {
  let s;
  for (let i = 0; i < 30 && !s; i++) {
    const r = await api('GET', `/api/shipments/by-vendor-order/${vendorOrderId}`, { token: vendorToken });
    if (r.status === 200) s = r.data;
    else await sleep(1000);
  }
  if (!s) throw new Error(`no shipment for ${vendorOrderId}`);
  await api('POST', `/api/shipments/${s.id}/ready`, { token: vendorToken, body: {} });
  await must('POST', `/api/shipments/${s.id}/ship`, { token: vendorToken, body: { tracking_number: `PW049${Date.now()}` } });
  await must('POST', `/api/shipments/${s.id}/deliver`, { token: vendorToken, body: { note: 'rehearsal delivery' } });
}
async function waitVendorOrder(buyerToken, orderId, status) {
  for (let i = 0; i < 40; i++) {
    const { order } = await vendorOrderOf(buyerToken, orderId);
    const vo = order.vendor_orders[0];
    if (vo.status === status) return vo;
    await sleep(1000);
  }
  throw new Error(`vendor order of ${orderId} not ${status}`);
}

// Works on both schemas: before 000030 the snapshot columns do not exist.
const orderItems = (orderId) => sql('order_db', `SELECT to_jsonb(oi) FROM order_items oi JOIN vendor_orders vo ON vo.id=oi.vendor_order_id
  WHERE vo.order_id='${orderId}' ORDER BY oi.price_amount DESC`)
  .split('\n').filter(Boolean).map((l) => {
    const r = JSON.parse(l);
    return { id: r.id, variant: r.variant_id, price: Number(r.price_amount), qty: Number(r.quantity), subtotal: Number(r.subtotal_amount),
      source: r.pricing_source, pv: r.catalog_product_version == null ? '' : String(r.catalog_product_version), vv: r.catalog_variant_version == null ? '' : String(r.catalog_variant_version) };
  });
const variantRow = (id) => {
  const [price, version, source] = sql('catalog_db', `SELECT price_amount, price_version, price_source FROM product_variants WHERE id='${id}'`).split('|');
  return { price: Number(price), version: Number(version), source };
};
const outbox = () => sql('catalog_db', `SELECT count(*) FILTER (WHERE attempts<5) || '|' || count(*) FILTER (WHERE attempts>=5) FROM variant_price_outbox`).split('|').map(Number);
async function drainOutbox(seconds = 30) {
  for (let i = 0; i < seconds; i++) {
    const [pending, parked] = outbox();
    if (pending === 0 && parked === 0) return true;
    await sleep(1000);
  }
  return false;
}

// --- Phases -----------------------------------------------------------------

// Base release (before AF-10): the pilot shop sells a two-SKU product at one
// price and a product without SKUs; a buyer pays for one SKU and leaves the
// other in the cart across the upgrade.
async function legacy() {
  const adminToken = await admin();
  const vendorToken = await pilotShop(adminToken);
  if (!state.sku) state.sku = await createProduct(adminToken, vendorToken, `PW049 Ao hai SKU ${state.run}`, 100000, 2);
  save();
  if (!state.plain) state.plain = await createProduct(adminToken, vendorToken, `PW049 San pham thuong ${state.run}`, 70000, 0);
  save();
  for (const p of [state.sku, state.plain]) {
    const images = await must('GET', `/api/catalog/products/${p.id}/images`, { token: vendorToken });
    if ((images.images || images || []).length === 0) await uploadImage(vendorToken, p.id);
    await approveProduct(adminToken, vendorToken, p.id);
  }
  const b = await account('buyer', 'buyer');
  const token = await login(b.email, b.password);
  await buyerAddress(token);
  await clearCart(token);
  check('legacy SKU added to cart', (await addToCart(token, state.sku.id, state.sku.variants[0])).status < 300);
  const bought = await purchase(token);
  state.legacyOrder = bought.orderId;
  const items = orderItems(bought.orderId);
  check('legacy order paid at product price', items.length === 1 && items[0].price === 100000 && bought.intent.amount === bought.order.total_amount,
    { order: bought.orderId, item_price: items[0].price, intent: bought.intent.amount, total: bought.order.total_amount });
  // Left in the cart across the upgrade; no pricing reference exists yet.
  check('legacy cart line kept for upgrade', (await addToCart(token, state.sku.id, state.sku.variants[1], 2)).status < 300);
  save();
}

// Target release, capability off: one price per product, SKU writers sync,
// the legacy cart line re-confirms, old orders untouched.
async function flagOff() {
  const caps = await must('GET', '/api/catalog/capabilities');
  check('capability reported off', caps.variant_pricing_enabled === false);
  const detail = await productDetail(state.sku.slug);
  // Live prices, so a rerun of this phase checks the same rules.
  const p0 = detail.price_amount;
  check('range collapses to product price', detail.min_price === p0 && detail.max_price === p0, { price: p0, min: detail.min_price, max: detail.max_price });
  const backfill = sql('catalog_db', `SELECT count(*) FROM variant_price_audit WHERE variant_id IN ('${state.sku.variants.join("','")}')
    AND price_version=1 AND source='product_backfill_v1' AND new_price=100000`);
  check('backfill copied the old product price with audit', backfill === '2', { backfill });
  const [a, b] = state.sku.variants.map(variantRow);
  const adminToken = await admin();
  const v = state.accounts.vendor;
  const vendorToken = await login(v.email, v.password);
  const diff = await api('PATCH', `/api/catalog/products/${state.sku.id}/variants/${state.sku.variants[0]}/price`, { token: vendorToken, body: { price_amount: p0 + 30000, expected_version: a.version } });
  check('different SKU price refused while off', diff.status === 409, { status: diff.status, code: errorCode(diff) });
  const before = variantRow(state.sku.variants[0]);
  check('refused write left price and version', before.price === p0 && before.version === a.version, { before });

  // Product price edit with SKUs: every SKU follows, versioned and audited.
  const p1 = p0 + 20000;
  await ensurePackagingRule(adminToken, state.category);
  const editor = await must('GET', `/api/catalog/products/${state.sku.id}/editor`, { token: vendorToken });
  await must('PATCH', `/api/catalog/products/${state.sku.id}/content`, {
    token: vendorToken,
    body: { name: editor.product.name, description: editor.product.description, price_amount: p1, version: editor.product.version, attributes: attributeValues(state.category.template) },
  });
  const [a2, b2] = state.sku.variants.map(variantRow);
  const audits = sql('catalog_db', `SELECT count(*) FROM variant_price_audit WHERE variant_id IN ('${state.sku.variants.join("','")}') AND new_price=${p1}`);
  check('product price edit synced every SKU', a2.price === p1 && b2.price === p1 && a2.version === a.version + 1 && b2.version === b.version + 1 && audits === '2',
    { a: a2, b: b2, audits });
  const status = sql('catalog_db', `SELECT status FROM products WHERE id='${state.sku.id}'`);
  check('price edit follows moderation (back to draft)', status === 'draft', { status });
  await approveProduct(adminToken, vendorToken, state.sku.id);
  check('outbox drained', await drainOutbox());

  // The cart line from the old release has no pricing reference: re-confirm.
  const bt = await login(state.accounts.buyer.email, state.accounts.buyer.password);
  let c = await cart(bt);
  const legacyLine = c.items.find((l) => l.variant_id === state.sku.variants[1]);
  check('legacy cart line needs confirmation', legacyLine && !c.checkout_ready && legacyLine.price_amount === p1,
    { line: legacyLine && { state: legacyLine.state, changed: legacyLine.price_changed, seen: legacyLine.seen_pricing_version, price: legacyLine.price_amount }, ready: c.checkout_ready });
  // Quantity change alone must not accept the new price.
  await must('PATCH', `/api/cart/items/${state.sku.id}?variant_id=${state.sku.variants[1]}`, { token: bt, body: { quantity: 1, expected_version: c.version } });
  c = await cart(bt);
  check('quantity change keeps confirmation pending', !c.checkout_ready, { ready: c.checkout_ready });
  const bought = await purchase(bt);
  const items = orderItems(bought.orderId);
  check('flag-off SKU order at product price', items.length === 1 && items[0].price === p1 && bought.intent.amount === bought.order.total_amount,
    { order: bought.orderId, items, intent: bought.intent.amount, total: bought.order.total_amount });
  state.flagOffOrder = bought.orderId;
  const legacyItems = orderItems(state.legacyOrder);
  check('pre-upgrade order unchanged', legacyItems[0].price === 100000 && legacyItems[0].source === 'product' && legacyItems[0].pv === '', { legacyItems });
  save();
}

// Capability on, pilot shop only: two SKU prices, CAS, buyer journey to
// capture, delivery, return and refund; price edits after purchase.
async function pilot() {
  const caps = await must('GET', '/api/catalog/capabilities');
  check('capability reported on', caps.variant_pricing_enabled === true);
  const adminToken = await admin();
  const vendorToken = await login(state.accounts.vendor.email, state.accounts.vendor.password);
  const [A, B] = state.sku.variants;
  const a = variantRow(A);
  // Two editors with the same version: exactly one wins.
  const race = await Promise.all([150000, 155000].map((price) =>
    api('PATCH', `/api/catalog/products/${state.sku.id}/variants/${A}/price`, { token: vendorToken, body: { price_amount: price, expected_version: a.version } })));
  const statuses = race.map((r) => r.status).sort();
  check('two editors, one CAS winner', statuses[0] === 200 && statuses[1] === 409, { statuses, codes: race.map(errorCode) });
  const won = variantRow(A);
  if (won.price !== 150000) {
    await must('PATCH', `/api/catalog/products/${state.sku.id}/variants/${A}/price`, { token: vendorToken, body: { price_amount: 150000, expected_version: won.version } });
  }
  for (const bad of [0, -1]) {
    const r = await api('PATCH', `/api/catalog/products/${state.sku.id}/variants/${B}/price`, { token: vendorToken, body: { price_amount: bad, expected_version: variantRow(B).version } });
    check(`non-positive SKU price ${bad} refused`, r.status === 400 || r.status === 422, { status: r.status });
  }
  await must('PATCH', `/api/catalog/products/${state.sku.id}/variants/${B}/price`, { token: vendorToken, body: { price_amount: 90000, expected_version: variantRow(B).version } });
  const audit = sql('catalog_db', `SELECT count(*) FROM variant_price_audit WHERE variant_id IN ('${A}','${B}') AND source<>'product_backfill_v1' AND actor_user_id IS NOT NULL AND new_price IN (150000,90000)`);
  check('SKU edits audited with actor', Number(audit) >= 2, { audit });
  check('SKU edit back to draft', sql('catalog_db', `SELECT status FROM products WHERE id='${state.sku.id}'`) === 'draft');
  const unsold = await api('GET', `/api/catalog/products/${state.sku.slug}`);
  check('draft revision not on storefront', unsold.status === 404, { status: unsold.status });
  await approveProduct(adminToken, vendorToken, state.sku.id);
  check('outbox drained after SKU edits', await drainOutbox());
  const detail = await productDetail(state.sku.slug);
  check('storefront shows SKU range', detail.min_price === 90000 && detail.max_price === 150000, { min: detail.min_price, max: detail.max_price });
  const listing = await must('GET', `/api/catalog/products?vendor_id=${state.vendorId}&limit=20`);
  const row = (listing.products || []).find((p) => p.id === state.sku.id);
  check('listing shows SKU range', row && row.min_price === 90000 && row.max_price === 150000, { row: row && { min: row.min_price, max: row.max_price } });

  const bt = await login(state.accounts.buyer.email, state.accounts.buyer.password);
  await clearCart(bt);
  // No SKU chosen: no final price, cannot buy the product as such.
  const noSku = await addToCart(bt, state.sku.id, null);
  check('product with SKUs needs a SKU', noSku.status >= 400, { status: noSku.status, code: errorCode(noSku) });
  const other = await addToCart(bt, state.plain.id, A);
  check('SKU of another product refused', other.status >= 400, { status: other.status, code: errorCode(other) });
  // A client price is either refused or ignored; the line is priced by Catalog.
  const fake = await api('POST', '/api/cart/items', { token: bt, body: { product_id: state.sku.id, variant_id: B, quantity: 1, price_amount: 1, unit_price: 1 } });
  if (fake.status >= 300) await addToCart(bt, state.sku.id, B, 1);
  await addToCart(bt, state.sku.id, A, 1);
  let c = await confirmPrices(bt);
  const lineA = c.items.find((l) => l.variant_id === A);
  const lineB = c.items.find((l) => l.variant_id === B);
  check('cart prices per SKU, client price not used', lineA.price_amount === 150000 && lineB.price_amount === 90000 && c.subtotal.amount === 240000,
    { a: lineA.price_amount, b: lineB.price_amount, subtotal: c.subtotal, fake_add_status: fake.status });

  // Price changes between preview and create: 409, no order, no hold.
  const p1 = await preview(bt);
  check('preview totals SKU prices', p1.status === 200 && p1.data.subtotal_amount === 240000, { subtotal: p1.data && p1.data.subtotal_amount, quote: p1.data && p1.data.quote_version });
  const ordersBefore = sql('order_db', `SELECT count(*) FROM orders WHERE buyer_id=(SELECT buyer_id FROM orders WHERE id='${state.legacyOrder}')`);
  const reservedBefore = sql('inventory_db', `SELECT coalesce(sum(reserved_quantity),0) FROM inventory_items WHERE variant_id IN ('${A}','${B}')`);
  await must('PATCH', `/api/catalog/products/${state.sku.id}/variants/${A}/price`, { token: vendorToken, body: { price_amount: 160000, expected_version: variantRow(A).version } });
  await approveProduct(adminToken, vendorToken, state.sku.id);
  const stale = await checkout(bt, c.version, p1.data);
  const ordersAfter = sql('order_db', `SELECT count(*) FROM orders WHERE buyer_id=(SELECT buyer_id FROM orders WHERE id='${state.legacyOrder}')`);
  const reservedAfter = sql('inventory_db', `SELECT coalesce(sum(reserved_quantity),0) FROM inventory_items WHERE variant_id IN ('${A}','${B}')`);
  check('price change after preview: 409 price_changed, nothing created', stale.status === 409 && /price_changed/.test(stale.text) && ordersBefore === ordersAfter && reservedBefore === reservedAfter,
    { status: stale.status, code: errorCode(stale), orders: [ordersBefore, ordersAfter], reserved: [reservedBefore, reservedAfter] });
  c = await cart(bt);
  check('cart asks to confirm the new price', !c.checkout_ready && c.items.find((l) => l.variant_id === A).price_changed);
  c = await confirmPrices(bt);
  const p2 = await preview(bt);
  const forged = await checkout(bt, c.version, p2.data, { expected_total_amount: p2.data.total_amount - 1000 });
  const ordersForged = sql('order_db', `SELECT count(*) FROM orders WHERE buyer_id=(SELECT buyer_id FROM orders WHERE id='${state.legacyOrder}')`);
  check('buyer total that differs from the quote refused', forged.status === 409 && ordersForged === ordersBefore, { status: forged.status, code: errorCode(forged) });
  const bought = await purchase(bt);
  const items = orderItems(bought.orderId);
  const shipping = bought.order.total_amount - 250000;
  check('order snapshots SKU prices and versions', items.length === 2 && items[0].price === 160000 && items[1].price === 90000 && items.every((i) => i.source === 'variant' && i.pv && i.vv),
    { order: bought.orderId, items });
  check('capture equals Order total', bought.intent.amount === bought.order.total_amount && shipping >= 0, { intent: bought.intent.amount, total: bought.order.total_amount, shipping });
  const captured = sql('payment_db', `SELECT amount || '|' || status FROM payment_intents WHERE order_id='${bought.orderId}' ORDER BY created_at DESC LIMIT 1`);
  check('provider amount matches', captured.split('|')[0] === String(bought.order.total_amount), { captured });
  state.pilotOrder = bought.orderId;
  save();

  // Price edits after purchase never touch the order.
  await must('PATCH', `/api/catalog/products/${state.sku.id}/variants/${B}/price`, { token: vendorToken, body: { price_amount: 95000, expected_version: variantRow(B).version } });
  await approveProduct(adminToken, vendorToken, state.sku.id);
  const after = orderItems(bought.orderId);
  check('order items unchanged after SKU edit', JSON.stringify(after) === JSON.stringify(items));
  const tamper = spawnSync('bash', [SCRIPT, 'sql', 'order_db', '-c', `UPDATE order_items SET price_amount=price_amount+1 WHERE id='${items[1].id}'`], { encoding: 'utf8' });
  check('snapshot trigger refuses edits', tamper.status !== 0 && /immutable/.test(tamper.stderr), { stderr: tamper.stderr.trim().split('\n')[0] });

  // Delivery, return of the cheaper SKU, refund at the purchase price.
  const { vendorOrderId } = await vendorOrderOf(bt, bought.orderId);
  await deliver(vendorToken, vendorOrderId);
  await waitVendorOrder(bt, bought.orderId, 'completed');
  const rr = await must('POST', `/api/orders/${bought.orderId}/return-requests`, { token: bt, body: { order_item_id: items[1].id, quantity: 1, reason: 'Rehearsal return of the 90.000 SKU' } });
  check('return amount uses purchase price', rr.refund_amount === 90000, { refund_amount: rr.refund_amount });
  await must('POST', `/api/orders/vendor/return-requests/${rr.id}/confirm`, { token: vendorToken, body: { note: 'ok' } });
  await must('POST', `/api/orders/admin/return-requests/${rr.id}/decision`, { token: adminToken, body: { approve: true, note: 'rehearsal' } });
  await must('POST', `/api/orders/vendor/return-requests/${rr.id}/receive`, { token: vendorToken, body: { restock: true, note: 'received intact' } });
  // Order hands the refund to Payment through a retried effect; allow for
  // its backoff after a slow first attempt.
  let refund;
  for (let i = 0; i < 150 && !refund; i++) {
    const list = await must('GET', '/api/payments/admin/refunds?limit=50', { token: adminToken });
    refund = (list.refunds || list.items || list).find((r) => r.order_id === bought.orderId);
    if (!refund) await sleep(1000);
  }
  check('refund requested for the return', refund && refund.amount === 90000, { refund: refund && { id: refund.id, amount: refund.amount, status: refund.status } });
  await must('POST', `/api/payments/admin/refunds/${refund.id}/resolve`, { token: adminToken, body: { outcome: 'succeeded', evidence_reference: 'PW049-SANDBOX', note: 'rehearsal mock refund' } });
  let returned;
  for (let i = 0; i < 150; i++) {
    returned = sql('order_db', `SELECT status FROM return_requests WHERE id='${rr.id}'`);
    if (returned === 'refunded') break;
    await sleep(1000);
  }
  check('return refunded at purchase price', returned === 'refunded', { status: returned });
  const restocked = sql('inventory_db', `SELECT count(*) FROM stock_movements WHERE reference_id='${rr.id}' OR reference_id LIKE '%${rr.id}%'`);
  check('return restocked once', restocked === '1', { movements: restocked });
  check('order items unchanged after refund', JSON.stringify(orderItems(bought.orderId)) === JSON.stringify(items));
  save();
}

// Broker outage: events wait in the outbox, park after five failures, the
// operator replays after recovery; the event id stays the same.
async function outage() {
  const adminToken = await admin();
  const vendorToken = await login(state.accounts.vendor.email, state.accounts.vendor.password);
  const [A] = state.sku.variants;
  check('outbox empty before drill', await drainOutbox());
  nats('stop');
  try {
    const v = variantRow(A);
    await must('PATCH', `/api/catalog/products/${state.sku.id}/variants/${A}/price`, { token: vendorToken, body: { price_amount: v.price + 1000, expected_version: v.version } });
    const version = variantRow(A).version;
    await sleep(3000);
    const [pending] = outbox();
    check('outbox holds the event while NATS is down', pending === 1, { pending });
    let parked = 0;
    for (let i = 0; i < 90 && parked === 0; i++) {
      [, parked] = outbox();
      if (parked === 0) await sleep(1000);
    }
    const ops = await must('GET', '/api/catalog/operations', { token: adminToken });
    check('event parked after retries, visible to operators', parked === 1, { parked, operations: ops });
    state.outage = { variant: A, version, eventId: `variant-${A}-v${version}` };
    save();
  } finally {
    nats('start');
  }
  await sleep(5000);
  const [, stillParked] = outbox();
  check('parked event waits for an operator', stillParked === 1);
  await must('POST', '/api/catalog/operations/replay', { token: adminToken, body: { kind: 'variant_price', id: A, reason: 'Đã khôi phục event bus (PW-049 rehearsal)' } });
  check('replay drains the outbox', await drainOutbox(60));
  const replayAudit = sql('catalog_db', `SELECT count(*) FROM product_audit_logs WHERE product_id='${state.sku.id}' AND action='replay_variant_price'`);
  check('replay audited', Number(replayAudit) >= 1, { replayAudit });
  // The stream keeps the published message under its deterministic id.
  const box = spawnSync('bash', [SCRIPT, 'stream-message', state.outage.eventId], { encoding: 'utf8' });
  check('published event keeps its id', box.status === 0 && box.stdout.includes(state.outage.eventId), { output: box.stdout.trim().slice(0, 300), err: box.stderr.trim().slice(0, 200) });
  await approveProduct(adminToken, vendorToken, state.sku.id);
}

// Rollback by turning the capability off: SKUs priced apart fail closed,
// ordinary products and single-price SKUs keep selling, history is kept.
async function rollback() {
  const caps = await must('GET', '/api/catalog/capabilities');
  check('capability reported off after rollback', caps.variant_pricing_enabled === false);
  const bt = await login(state.accounts.buyer.email, state.accounts.buyer.password);
  await clearCart(bt);
  const [A] = state.sku.variants;
  const add = await addToCart(bt, state.sku.id, A, 1);
  let blocked = add.status >= 400;
  if (!blocked) {
    const c = await cart(bt);
    const p = await preview(bt);
    blocked = !c.checkout_ready || p.status >= 400 || !p.data.ready;
  }
  check('SKU priced apart fails closed', blocked, { add: add.status, code: errorCode(add) });
  await clearCart(bt);
  await addToCart(bt, state.plain.id, null, 1);
  const bought = await purchase(bt);
  const items = orderItems(bought.orderId);
  check('ordinary product still sells', items.length === 1 && items[0].price === 70000 && items[0].source === 'product', { order: bought.orderId, items });
  const pilotItems = orderItems(state.pilotOrder);
  check('pilot order history kept by new readers', pilotItems.length === 2 && pilotItems.every((i) => i.source === 'variant'), { pilotItems });
  const o = await must('GET', `/api/orders/${state.pilotOrder}`, { token: bt });
  check('buyer still reads the pilot order', o.id === state.pilotOrder && o.total_amount > 0, { status: o.status, total: o.total_amount });
  // SKUs at the product price keep selling while the capability is off.
  const adminToken = await admin();
  const vendorToken = await login(state.accounts.vendor.email, state.accounts.vendor.password);
  if (!state.singlePrice) {
    state.singlePrice = await createProduct(adminToken, vendorToken, `PW049 SKU mot gia ${state.run}`, 85000, 2);
    save();
  }
  await approveProduct(adminToken, vendorToken, state.singlePrice.id);
  await clearCart(bt);
  await addToCart(bt, state.singlePrice.id, state.singlePrice.variants[1], 1);
  const single = await purchase(bt);
  const singleItems = orderItems(single.orderId);
  check('single-price SKU still sells', singleItems.length === 1 && singleItems[0].price === 85000 && single.intent.amount === single.order.total_amount,
    { order: single.orderId, items: singleItems });

  // Obligations already created continue: return and refund the 160.000 SKU.
  const itemA = pilotItems.find((i) => i.variant === A);
  if (!state.rollbackReturn) {
    const rr = await must('POST', `/api/orders/${state.pilotOrder}/return-requests`, { token: bt, body: { order_item_id: itemA.id, quantity: 1, reason: 'Rehearsal return after rollback' } });
    state.rollbackReturn = rr.id;
    save();
    check('return after rollback priced from the order item', rr.refund_amount === itemA.price, { refund_amount: rr.refund_amount, item_price: itemA.price });
    await must('POST', `/api/orders/vendor/return-requests/${rr.id}/confirm`, { token: vendorToken, body: { note: 'ok' } });
    await must('POST', `/api/orders/admin/return-requests/${rr.id}/decision`, { token: adminToken, body: { approve: true, note: 'rehearsal' } });
    await must('POST', `/api/orders/vendor/return-requests/${rr.id}/receive`, { token: vendorToken, body: { restock: true, note: 'received intact' } });
  }
  let refund;
  for (let i = 0; i < 150 && !refund; i++) {
    const list = await must('GET', '/api/payments/admin/refunds?limit=50', { token: adminToken });
    refund = list.find((r) => r.order_id === state.pilotOrder && r.amount === itemA.price);
    if (!refund) await sleep(1000);
  }
  if (refund && refund.status !== 'succeeded') {
    await must('POST', `/api/payments/admin/refunds/${refund.id}/resolve`, { token: adminToken, body: { outcome: 'succeeded', evidence_reference: 'PW049-SANDBOX', note: 'rehearsal mock refund after rollback' } });
  }
  let returned;
  for (let i = 0; i < 150; i++) {
    returned = sql('order_db', `SELECT status FROM return_requests WHERE id='${state.rollbackReturn}'`);
    if (returned === 'refunded') break;
    await sleep(1000);
  }
  const refundedTotal = sql('order_db', `SELECT refunded_amount FROM orders WHERE id='${state.pilotOrder}'`);
  check('refund after rollback completes at the sold price', refund && refund.amount === itemA.price && returned === 'refunded' && Number(refundedTotal) === 250000,
    { refund: refund && refund.amount, status: returned, order_refunded: refundedTotal });

  const differentiated = sql('catalog_db', `SELECT count(*) FROM product_variants v JOIN products p ON p.id=v.product_id WHERE v.price_amount<>p.price_amount`);
  check('SKU prices left as sold (no mass reset)', Number(differentiated) >= 2, { differentiated });
  const down = spawnSync('bash', [SCRIPT, 'down-guard'], { encoding: 'utf8' });
  check('down migrations refuse after use', down.status === 0, { output: down.stdout.trim().split('\n').slice(-4) });
}

const phases = { legacy, 'flag-off': flagOff, pilot, outage, rollback };
phase = process.argv[2];
if (!phases[phase]) {
  console.error(`phases: ${Object.keys(phases).join(', ')}`);
  process.exit(2);
}
// A rerun of a phase replaces its earlier results.
state.checks = state.checks.filter((c) => c.phase !== phase);
try {
  await phases[phase]();
  console.log(`[${phase}] done`);
} catch (e) {
  console.error(`[${phase}] stopped: ${e.message}`);
  process.exitCode = 1;
} finally {
  save();
}
