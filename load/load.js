// k6 load test (make load). Read-heavy mix of the screens people use all
// day, plus logins (argon2id, CPU-bound) at a lower rate. Writes are left
// out on purpose so the run does not change the demo data.
import http from "k6/http";
import { check } from "k6";
import { Rate } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://api:8080";
const PASSWORD = __ENV.DEMO_PASSWORD || "Demo.2026!";
const errors = new Rate("errors");

export const options = {
  scenarios: {
    browse: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "15s", target: 25 },
        { duration: "60s", target: 50 },
        { duration: "15s", target: 0 },
      ],
      exec: "browse",
    },
    logins: {
      executor: "constant-arrival-rate",
      rate: 5,
      timeUnit: "1s",
      duration: "90s",
      preAllocatedVUs: 10,
      exec: "loginOnly",
    },
  },
  thresholds: {
    errors: ["rate<0.01"],
    "http_req_duration{name:employees}": ["p(95)<300"],
    "http_req_duration{name:balance}": ["p(95)<300"],
    "http_req_duration{name:timesheet_month}": ["p(95)<500"],
    "http_req_duration{name:payroll_results}": ["p(95)<300"],
    "http_req_duration{name:login}": ["p(95)<800"],
  },
  summaryTrendStats: ["med", "p(95)", "p(99)", "max"],
};

function login(email) {
  const r = http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({ email, password: PASSWORD }), {
    headers: { "Content-Type": "application/json" },
    tags: { name: "login" },
  });
  const ok = check(r, { "login 200": (x) => x.status === 200 });
  errors.add(!ok);
  return ok ? r.json("access_token") : null;
}

export function setup() {
  const tokens = {};
  for (const u of ["calisan", "yonetici", "ik", "bordro"]) tokens[u] = login(`${u}@bordroplus.test`);
  const auth = { headers: { Authorization: `Bearer ${tokens.ik}` } };
  const list = http.get(`${BASE}/api/v1/employees?limit=200`, auth).json("items");
  const ids = list.map((e) => e.id);
  const me = http.get(`${BASE}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${tokens.calisan}` } }).json("employee_id");
  return { tokens, ids, me };
}

function get(path, token, name) {
  const r = http.get(`${BASE}${path}`, { headers: { Authorization: `Bearer ${token}` }, tags: { name } });
  const ok = check(r, { [`${name} 200`]: (x) => x.status === 200 });
  errors.add(!ok);
  return r;
}

const pick = (a) => a[Math.floor(Math.random() * a.length)];

export function browse(d) {
  const id = pick(d.ids);
  switch (Math.floor(Math.random() * 6)) {
    case 0:
      get("/api/v1/employees?limit=50", d.tokens.yonetici, "employees"); // manager scope: ltree sub-tree
      break;
    case 1:
      get(`/api/v1/leave/employees/${id}/balance`, d.tokens.ik, "balance");
      break;
    case 2:
      get(`/api/v1/timesheet/employees/${id}/months/2026-09`, d.tokens.ik, "timesheet_month"); // pairs ~45 days of punches
      break;
    case 3:
      get(`/api/v1/payroll/employees/${id}/results?year=2025`, d.tokens.bordro, "payroll_results");
      break;
    case 4:
      get(`/api/v1/leave/requests?for_approval=true&limit=50`, d.tokens.yonetici, "leave_queue");
      break;
    default:
      get("/api/v1/system/status", d.tokens.calisan, "status");
  }
}

export function loginOnly() {
  login("calisan@bordroplus.test");
}

export function handleSummary(data) {
  const rows = [];
  for (const [k, v] of Object.entries(data.metrics)) {
    const m = k.match(/^http_req_duration\{name:(.+)\}$/);
    if (m && v.values) rows.push([m[1], v.values["med"], v.values["p(95)"], v.values["p(99)"], v.values["max"]]);
  }
  const reqs = data.metrics.http_reqs.values;
  const failed = data.metrics.http_req_failed.values.rate;
  let out = "\n| Uç nokta | p50 (ms) | p95 (ms) | p99 (ms) | maks (ms) |\n|---|---:|---:|---:|---:|\n";
  for (const r of rows.sort()) out += `| ${r[0]} | ${r.slice(1).map((x) => (x ?? 0).toFixed(1)).join(" | ")} |\n`;
  out += `\nToplam ${reqs.count} istek, ${reqs.rate.toFixed(1)} istek/sn, hata oranı %${(failed * 100).toFixed(2)}\n`;
  return { stdout: out };
}
