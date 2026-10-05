// Read path: listing, search, product pages, at rising rates.
//   bash deploy/loadtest/run.sh k6 browse.js -e START_RATE=5 -e STEP_RATE=5 -e STEPS=8
import { catalogSample } from './lib.js';
import { browse } from './flows.js';
import { steps } from './lib.js';

export const options = {
  setupTimeout: '5m',
  scenarios: {
    browse: { executor: 'ramping-arrival-rate', exec: 'run', startRate: 1, timeUnit: '1s', preAllocatedVUs: 50, maxVUs: 600, stages: steps() },
  },
  thresholds: {
    'http_req_duration{name:catalog listing}': ['p(95)<500'],
    'http_req_duration{name:catalog detail}': ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  return catalogSample();
}

export function run(sample) {
  browse(sample);
}

export function handleSummary(d) {
  return { [`/results/browse-${__ENV.RUN_ID || 'run'}.json`]: JSON.stringify(d, null, 1) };
}
