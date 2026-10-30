// k6 yük testi: make load
// Senaryolar: okuma ağırlıklı personel trafiği + hastane talepleri (yazma, idempotans anahtarlı).
import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const PW = __ENV.DEMO_PASSWORD || 'Demo1234!';
const GROUPS = ['A+', 'A+', 'O+', 'B+', 'AB+', 'A-', 'O-'];

export const options = {
  scenarios: {
    staff_reads: { executor: 'constant-vus', vus: 20, duration: '60s', exec: 'reads' },
    hospital_requests: { executor: 'constant-arrival-rate', rate: 10, timeUnit: '1s', duration: '60s', preAllocatedVUs: 20, exec: 'requests' },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{kind:read}': ['p(95)<300'],
    'http_req_duration{kind:write}': ['p(95)<500'],
  },
};

function login(email) {
  const r = http.post(`${BASE}/auth/login`, JSON.stringify({ email, password: PW }), { headers: { 'Content-Type': 'application/json' } });
  return r.json('access_token');
}

export function setup() {
  return { bank: login('bankasorumlusu@damarbagis.local'), hosp: login('hastane@damarbagis.local'), nurse: login('hemsire@damarbagis.local') };
}

export function reads(t) {
  const h = { headers: { Authorization: `Bearer ${t.bank}` }, tags: { kind: 'read' } };
  const n = { headers: { Authorization: `Bearer ${t.nurse}` }, tags: { kind: 'read' } };
  const res = [
    http.get(`${BASE}/api/v1/stock`, h),
    http.get(`${BASE}/api/v1/products?status=available&component=RBC&sort=expiry&limit=50`, h),
    http.get(`${BASE}/api/v1/donors?q=yil&limit=25`, n),
    http.get(`${BASE}/api/v1/requests?status=open,partial,reserved&limit=50`, h),
  ];
  res.forEach((r) => check(r, { '200': (x) => x.status === 200 }));
  sleep(0.2);
}

export function requests(t) {
  const body = JSON.stringify({
    component: 'RBC', group: GROUPS[Math.floor(Math.random() * GROUPS.length)], units: 1,
    urgency: Math.random() < 0.05 ? 'emergency' : 'routine', patient_ref: `K6-${__VU}-${__ITER}`,
  });
  const r = http.post(`${BASE}/api/v1/requests`, body, {
    headers: { Authorization: `Bearer ${t.hosp}`, 'Content-Type': 'application/json', 'Idempotency-Key': `k6-${__VU}-${__ITER}-${Date.now()}` },
    tags: { kind: 'write' },
  });
  check(r, { '201': (x) => x.status === 201 });
}
