// Write path: whole purchases (cart, preview, checkout, payment) at rising
// rates. LINES and VENDORS shape the basket (default 3 lines, 2 shops).
//   bash deploy/loadtest/run.sh k6 checkout.js -e LINES=5 -e VENDORS=3
import { catalogSample, steps } from './lib.js';
import { purchase } from './flows.js';

export const options = {
  setupTimeout: '5m',
  scenarios: {
    checkout: { executor: 'ramping-arrival-rate', exec: 'run', startRate: 1, timeUnit: '1s', preAllocatedVUs: 30, maxVUs: 400, stages: steps() },
  },
  thresholds: {
    'http_req_duration{name:checkout}': ['p(95)<2000'],
    checkout_flow_duration: ['p(95)<5000'],
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  return catalogSample();
}

export function run(sample) {
  purchase(sample);
}

export function handleSummary(d) {
  return { [`/results/checkout-${__ENV.RUN_ID || 'run'}.json`]: JSON.stringify(d, null, 1) };
}
