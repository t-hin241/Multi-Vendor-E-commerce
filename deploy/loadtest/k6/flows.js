// User journeys used by the scenarios. Request names (tags) are the
// operations reported in results and in Prometheus (k6_http_req_duration).
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { basket, buyer, data, get, ok, pick, send } from './lib.js';

const SEARCH_TERMS = ['ao', 'giay', 'dien thoai', 'tai nghe', 'sach', 'balo', 'dong ho', 'kem'];
export const checkoutFlow = new Trend('checkout_flow_duration', true);
export const checkoutOutcome = new Counter('checkout_outcome');

function think(min, max) {
  if (__ENV.NO_THINK !== '1') sleep(min + Math.random() * (max - min));
}

// A shopper looking around: a listing page (sometimes a search), then a
// couple of product pages.
export function browse(sample) {
  const offset = 20 * Math.floor(Math.random() * 100);
  const query = Math.random() < 0.3 ? `&q=${encodeURIComponent(pick(SEARCH_TERMS))}` : '';
  ok(get(`/api/catalog/products?limit=20&offset=${offset}${query}`, query ? 'catalog search' : 'catalog listing'), 'listing');
  think(0.5, 1.5);
  for (let i = 0; i < 2; i++) {
    ok(get(`/api/catalog/products/${pick(sample.slugs)}`, 'catalog detail'), 'detail');
    think(0.5, 1.5);
  }
  if (Math.random() < 0.2) ok(get('/api/catalog/categories', 'catalog categories'), 'categories');
}

function fillCart(sample, token, lines, vendors) {
  send('DELETE', '/api/cart', undefined, 'cart clear', token);
  for (const it of basket(sample.items, lines, vendors)) {
    const body = { product_id: it.product, quantity: 1 };
    if (it.variant) body.variant_id = it.variant;
    ok(send('POST', '/api/cart/items', body, 'cart add', token), 'cart add');
  }
  const view = get('/api/cart', 'cart view', token);
  ok(view, 'cart view');
  return data(view);
}

// Adds to the cart and looks at it, without buying.
export function cart(sample) {
  const b = buyer();
  fillCart(sample, b.token, 1 + Math.floor(Math.random() * 3), 2);
  think(1, 3);
}

// The whole purchase: cart, preview, checkout, payment intent and the mock
// provider's success. Outcome counted by stage so failures are attributed.
export function purchase(sample) {
  const lines = Number(__ENV.LINES || 3);
  const vendors = Number(__ENV.VENDORS || 2);
  const b = buyer();
  const start = Date.now();
  const c = fillCart(sample, b.token, lines, vendors);
  if (!c || !c.checkout_ready) {
    if (__ENV.DEBUG === '1' && c) {
      console.warn(`cart not ready: ${JSON.stringify((c.items || []).map((l) => [l.state, l.stock_status, l.price_changed, l.available]))} degraded=${JSON.stringify(c.degraded)}`);
    }
    checkoutOutcome.add(1, { stage: 'cart_not_ready' });
    return;
  }
  const preview = send('POST', '/api/orders/checkout/preview', { address_id: b.addressId }, 'checkout preview', b.token);
  if (!ok(preview, 'preview')) {
    checkoutOutcome.add(1, { stage: 'preview' });
    return;
  }
  const order = send('POST', '/api/orders/checkout', { address_id: b.addressId, cart_version: c.version }, 'checkout', b.token,
    { 'Idempotency-Key': `lt-${__VU}-${__ITER}-${start}` });
  if (!ok(order, 'checkout')) {
    checkoutOutcome.add(1, { stage: `checkout_${order.status}` });
    return;
  }
  const orderId = data(order).id || data(order).order_id;
  const intent = send('POST', '/api/payments/intents', { order_id: orderId }, 'payment intent', b.token);
  if (!ok(intent, 'intent')) {
    checkoutOutcome.add(1, { stage: `intent_${intent.status}` });
    return;
  }
  const paid = send('POST', `/api/payments/intents/${data(intent).id}/simulate`, { outcome: 'succeeded' }, 'payment simulate', b.token);
  if (!ok(paid, 'simulate')) {
    checkoutOutcome.add(1, { stage: `simulate_${paid.status}` });
    return;
  }
  checkoutFlow.add(Date.now() - start);
  checkoutOutcome.add(1, { stage: 'paid' });
  think(1, 3);
}
