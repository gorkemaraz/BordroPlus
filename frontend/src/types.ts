// Response shapes of the API (amounts are decimal strings).

export type Page<T> = { items: T[]; next_cursor?: string };

export type SystemStatus = {
  server_time: string;
  active_employees: number;
  punches_today: number;
  pending_leave_requests: number;
  open_punch_proposals: number;
  simulator: null | {
    seed: number;
    employees: number;
    history_from: string;
    history_to: string;
    loaded_at: string;
    punches_loaded: number;
    last_tick_at: string | null;
    live_punches: number;
  };
};

export type Employee = {
  id: number;
  employee_no: string;
  first_name: string;
  last_name: string;
  title: string;
  org_unit_id: number;
  org_unit_name: string;
  org_unit_path: string;
  hire_date: string;
  termination_date: string | null;
  salary?: { valid_from: string; amount: string } | null;
  salary_masked?: boolean;
};

export type Unit = {
  id: number;
  parent_id: number | null;
  code: string;
  name: string;
  path: string;
  depth: number;
  manager_id: number | null;
  manager_name: string | null;
  employee_count: number;
};

export type Balance = {
  employee_id: number;
  as_of: string;
  hire_date: string;
  service_years: number;
  balance: number;
  pending: number;
  available: number;
  carried_over: number;
  current_accrual: number;
  total_accrued: number;
  total_used: number;
  total_refunded: number;
  next_accrual: { date: string; days: number } | null;
};

export type LedgerRow = {
  id: number;
  kind: string;
  days: number;
  effective_date: string;
  leave_request_id: number | null;
  note: string;
  running_balance: number;
};

export type LeaveRequest = {
  id: number;
  employee_id: number;
  employee_name: string;
  leave_type: string;
  start_date: string;
  end_date: string;
  half_day: string;
  days: number;
  status: string;
  reason: string;
  decision_note: string;
  created_at: string;
};

export type Delegation = {
  id: number;
  manager_employee_id: number;
  manager_name: string;
  delegate_employee_id: number;
  delegate_name: string;
  start_date: string;
  end_date: string;
  reason: string;
  revoked_at: string | null;
  active: boolean;
};

export type Segment = { date: string; kind: "normal" | "rest_day" | "holiday"; minutes: number };
export type Session = {
  in: string;
  out: string;
  work_date: string;
  gross_minutes: number;
  break_minutes: number;
  net_minutes: number;
  segments: Segment[];
};
export type Anomaly = { kind: string; suggestion_key: string; punch_id: number; punch_at: string; suggested_at: string | null; reason: string };
export type Correction = {
  id: number;
  employee_id: number;
  employee_name: string;
  kind: string;
  target_punch_at: string;
  target_direction: string;
  proposed_at: string | null;
  work_date: string;
  reason: string;
  status: string;
  applied_at: string | null;
};
export type Days = {
  days: { date: string; net_minutes: number; sessions: Session[] }[];
  anomalies: Anomaly[];
  corrections: Correction[];
};
export type Totals = {
  normal_minutes: number;
  overtime_minutes: number;
  rest_day_minutes: number;
  holiday_minutes: number;
  worked_days: number;
};
export type MonthSummary = {
  month: string;
  from: string;
  to: string;
  status: "open" | "approved";
  rule_version: string;
  weeks: (Totals & { week_start: string; week_end: string })[];
  totals: Totals;
  open_anomalies: number;
  year_overtime_minutes: number;
  overtime_cap_exceeded: boolean;
  multipliers_pct: { overtime: number; rest_day: number; holiday: number };
};

export type Period = {
  period: string;
  status: "draft" | "review" | "approved" | "closed";
  rule_version: string | null;
  employee_count: number;
  totals: { gross?: string; net?: string; income_tax?: string; employer_cost?: string };
  digest: string;
  last_run_at: string | null;
  last_run_ms: number | null;
  last_run_failures: number;
  submitted_at: string | null;
  approved_at: string | null;
  closed_at: string | null;
  review_note: string;
};

export type RunReport = {
  period: string;
  employees: number;
  changed: number;
  unchanged: number;
  failures: { employee_id: number; error: string }[];
  duration_ms: number;
  workers: number;
  digest: string;
};

export type PayLine = { code: string; label: string; amount: string; formula: string };
export type PayResult = {
  rule_version: string;
  gross: string;
  net: string;
  income_tax: string;
  employer_cost: string;
  cum_tax_base: string;
  lines: PayLine[];
};
export type PaySummary = {
  period: string;
  gross: string;
  net: string;
  income_tax: string;
  cum_tax_base: string;
  employer_cost: string;
};
export type Payslip = {
  period: string;
  period_status: string;
  final: boolean;
  employee: { id: number; employee_no: string; name: string; title: string; unit: string };
  paid_days: number;
  lines: PayLine[];
  gross: string;
  total_deductions: string;
  net: string;
  notes: string[];
};
export type PayrollItem = {
  id: number;
  employee_id: number;
  kind: "bonus" | "advance" | "loan";
  label: string;
  amount: string;
  instalments: number;
  start_period: string;
  schedule: { period: string; amount: string }[];
  cancelled_at: string | null;
};

export type AuditRecord = {
  id: number;
  occurred_at: string;
  actor_user_id: number | null;
  actor_role: string | null;
  action: string;
  entity_type: string;
  entity_id: string | null;
  old_value: unknown;
  new_value: unknown;
  ip: string | null;
};
