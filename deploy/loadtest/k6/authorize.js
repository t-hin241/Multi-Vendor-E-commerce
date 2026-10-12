// PW-020: real Vendor/Postgres/Identity and the production shopaccess client.
import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Rate, Trend } from 'k6/metrics';

const target = __ENV.TARGET || 'vendor';
const expected = __ENV.EXPECT || 'allowed';
const probe = 'http://authorize-probe:8080';
const availability = new Rate('authorization_available');
const correct = new Rate('authorization_correct');
const latency = new Trend('authorization_latency', true);
const p95 = Number(__ENV.P95_MS || 250);
const p99 = Number(__ENV.P99_MS || 500);
if (!['vendor', 'client'].includes(target) || !['allowed','unavailable','denied'].includes(expected)) throw new Error('Invalid test mode');

export const options = {
  scenarios: { authorize: { executor: 'constant-arrival-rate', rate: Number(__ENV.RATE || 40), timeUnit: '1s', duration: __ENV.DURATION || '2m', preAllocatedVUs: 150, maxVUs: 400 } },
  thresholds: {
    authorization_correct: ['rate==1'],
    authorization_available: [expected === 'unavailable' ? 'rate==0' : 'rate>0.999'],
    authorization_latency: [`p(95)<${p95}`, `p(99)<${p99}`],
    dropped_iterations: ['count==0'],
    checks: ['rate==1'],
  },
  summaryTrendStats: ['avg','p(50)','p(95)','p(99)','max'],
};

function setFault(identity, vendor) {
  const r = http.post(probe + '/fault', JSON.stringify({identity_ms:identity,vendor_ms:vendor}), {headers:{'Content-Type':'application/json'},tags:{name:'fault control'}});
  if (r.status !== 204) throw new Error('Fault control failed');
}
export function setup() { setFault(Number(__ENV.IDENTITY_DELAY_MS || 0), Number(__ENV.VENDOR_DELAY_MS || 0)); }
export function teardown() { setFault(0,0); }

export default function () {
  const n = (exec.scenario.iterationInTest % 32) + 1;
  const staff = expected === 'denied' || Math.floor(exec.scenario.iterationInTest / 32) % 2 === 1;
  const suffix = (x) => String(x).padStart(12, '0');
  const vendorID = '02000001-0000-4000-8000-' + suffix(n);
  const permission = expected === 'denied' ? 'finance.read' : 'orders.read';
  const body = JSON.stringify({ actor_user_id: '02000000-0000-4000-8000-' + suffix(staff ? n+32 : n), vendor_id: vendorID, permission });
  const headers = {'Content-Type':'application/json','X-Request-ID':`pw020-${__ENV.RUN_ID || 'run'}-${exec.scenario.iterationInTest}`};
  if (target === 'vendor') {
    headers['X-Service-Name'] = 'catalog';
    headers['X-Identity-Service-Key'] = __ENV.CATALOG_INTERNAL_KEY;
  }
  const r = http.post(target === 'vendor' ? 'http://vendor:8082/internal/vendors/authorize' : probe+'/check', body, {headers,tags:{name:'authorize',actor:staff?'staff':'owner'},timeout:'6s'});
  let b = {};
  try { b = r.json(); } catch (_) { /* Count invalid envelopes as failures. */ }
  const unavailable = r.status === 503 && b.error && b.error.code === 'authorization_unavailable';
  const allowed = r.status === 200 && b.data && b.data.allowed === true && b.data.membership_version > 0 && b.data.role === (staff ? 'staff' : 'owner') && (target === 'client' || (b.data.vendor_id === vendorID && b.data.status === 'approved'));
  const denied = target === 'vendor' ? r.status === 200 && b.data && b.data.allowed === false : r.status === 403 && b.error && b.error.code === 'permission_denied';
  const pass = expected === 'allowed' ? allowed : expected === 'unavailable' ? unavailable : denied;
  correct.add(Boolean(pass));
  availability.add(r.status === 200 || r.status === 403);
  latency.add(r.timings.duration);
  check(r, {'expected authorization result': () => Boolean(pass)});
}

export function handleSummary(data) {
  const run = __ENV.RUN_ID || 'run';
  const summary = {
    run, target, expected,
    latency: data.metrics.authorization_latency && data.metrics.authorization_latency.values,
    available: data.metrics.authorization_available && data.metrics.authorization_available.values,
    correct: data.metrics.authorization_correct && data.metrics.authorization_correct.values,
    dropped: data.metrics.dropped_iterations && data.metrics.dropped_iterations.values,
  };
  data.pw020 = {
    run, target, expected, completed_at_utc: new Date().toISOString(),
    rate: Number(__ENV.RATE || 40), duration: __ENV.DURATION || '2m',
    identity_delay_ms: Number(__ENV.IDENTITY_DELAY_MS || 0),
    vendor_delay_ms: Number(__ENV.VENDOR_DELAY_MS || 0),
    p95_limit_ms: p95, p99_limit_ms: p99,
  };
  return {
    [`/results/authorize-${run}.json`]: JSON.stringify(data,null,2),
    stdout: JSON.stringify(summary) + '\n',
  };
}
