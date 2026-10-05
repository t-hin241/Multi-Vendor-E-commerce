// Realistic mix: 80% browsing, 15% carting, 5% buying, all rising together.
// The rate where p95 or errors break is the knee; soak at 70% of it with
// STEPS=1 STEP_DURATION=60m START_RATE=<70% of the knee>.
import { catalogSample, steps } from './lib.js';
import { browse, cart, purchase } from './flows.js';

const vus = (n) => ({ preAllocatedVUs: n, maxVUs: n * 10 });
export const options = {
  setupTimeout: '5m',
  scenarios: {
    browse: { executor: 'ramping-arrival-rate', exec: 'browsing', startRate: 1, timeUnit: '1s', ...vus(40), stages: steps(0.8) },
    cart: { executor: 'ramping-arrival-rate', exec: 'carting', startRate: 1, timeUnit: '1s', ...vus(10), stages: steps(0.15) },
    buy: { executor: 'ramping-arrival-rate', exec: 'buying', startRate: 1, timeUnit: '1s', ...vus(10), stages: steps(0.05) },
  },
  thresholds: {
    'http_req_duration{name:catalog listing}': ['p(95)<500'],
    'http_req_duration{name:checkout}': ['p(95)<2000'],
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  return catalogSample();
}
export function browsing(sample) {
  browse(sample);
}
export function carting(sample) {
  cart(sample);
}
export function buying(sample) {
  purchase(sample);
}

export function handleSummary(d) {
  return { [`/results/mixed-${__ENV.RUN_ID || 'run'}.json`]: JSON.stringify(d, null, 1) };
}
