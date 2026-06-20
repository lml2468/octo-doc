// k6 load test for octo-doc — alternative to `pnpm bench` (autocannon).
//
// Hits the doc-render hot path and asserts the non-functional latency targets
// (p50 ≤ 50ms, p99 ≤ 200ms). Point it at a running server; seed a doc first.
//
//   # 1. start a server and publish the fixture:
//   TOKEN=$(curl -s localhost:8080/api/admin/bootstrap | jq -r .token)
//   curl -H "Authorization: Bearer $TOKEN" -F file=@fixtures/hello.html -F slug=bench localhost:8080/api/docs
//   # 2. run k6:
//   k6 run -e BASE=http://localhost:8080 -e SLUG=bench bench/k6.js
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE || 'http://localhost:8080';
const SLUG = __ENV.SLUG || 'bench';

export const options = {
  scenarios: {
    render: { executor: 'constant-vus', vus: Number(__ENV.VUS || 50), duration: __ENV.DURATION || '30s' },
  },
  thresholds: {
    // Mirror the success criteria.
    http_req_duration: ['p(50)<50', 'p(99)<200'],
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const res = http.get(`${BASE}/d/${SLUG}/v/1`);
  check(res, {
    'status 200': (r) => r.status === 200,
    'has overlay': (r) => r.body.includes('window.__TDOC__'),
  });
}
