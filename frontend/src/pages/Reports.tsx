import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { api, openAuthed } from "../api/client";
import { addMonths, currentMonth, days, hours, money, month as monthName } from "../format";
import { Async, Badge, Card, ErrorBox, PageHeader, Table, Tabs } from "../ui";

type PayRow = { unit_path: string; unit_name: string; headcount: number; gross: string; sgk_employee: string; income_tax: string; stamp_tax: string; net: string; sgk_employer: string; employer_cost: string };

export default function ReportsPage() {
  const [params, setParams] = useSearchParams();
  const tabs = [
    { id: "payroll", label: "Bordro özeti" },
    { id: "cost", label: "İşveren maliyeti" },
    { id: "leave", label: "İzin kullanımı" },
    { id: "overtime", label: "Fazla mesai" },
  ];
  const tab = params.get("tab") ?? "payroll";
  return (
    <>
      <PageHeader title="Raporlar" sub="Raporlar birim toplamı verir; bireysel maaş içermez." />
      <Tabs tabs={tabs} value={tab} onChange={(t) => setParams({ tab: t })} />
      {tab === "payroll" && <PayrollSummary />}
      {tab === "cost" && <EmployerCost />}
      {tab === "leave" && <LeaveUsage />}
      {tab === "overtime" && <Overtime />}
    </>
  );
}

function LevelSelect({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(Number(e.target.value))} aria-label="Toplama düzeyi">
      <option value={1}>Şirket</option>
      <option value={2}>Departman</option>
      <option value={3}>Ekip</option>
    </select>
  );
}

function PayrollSummary() {
  const [period, setPeriod] = useState("2025-01");
  const [level, setLevel] = useState(2);
  const [err, setErr] = useState<unknown>(null);
  const q = useQuery({
    queryKey: ["rep-payroll", period, level],
    queryFn: () => api.get<{ period_status: string; items: PayRow[]; total: PayRow }>(`/reports/payroll-summary?period=${period}&level=${level}`),
  });
  return (
    <Card
      title="Birim bazında bordro"
      actions={
        <div className="row-actions">
          <input type="month" value={period} onChange={(e) => setPeriod(e.target.value)} aria-label="Dönem" />
          <LevelSelect value={level} onChange={setLevel} />
          <button className="btn small" onClick={() => openAuthed(`/reports/payroll-summary?period=${period}&level=${level}&format=csv`, `bordro-ozet-${period}.csv`).catch(setErr)}>CSV indir</button>
        </div>
      }
    >
      {err != null && <ErrorBox error={err} />}
      <Async q={q} isEmpty={(d) => d.items.length === 0} empty="Bu dönem için hesaplanmış bordro yok.">
        {(d) => (
          <>
            <p className="muted small">{monthName(period)} · <Badge status={d.period_status} /></p>
            <Table
              rows={[...d.items, d.total]}
              rowKey={(r) => r.unit_path || "total"}
              cols={[
                { label: "Birim", cell: (r) => (r.unit_path ? r.unit_name : <strong>Toplam</strong>) },
                { label: "Kişi", cell: (r) => r.headcount, num: true },
                { label: "Brüt", cell: (r) => money(r.gross), num: true },
                { label: "SGK işçi", cell: (r) => money(r.sgk_employee), num: true },
                { label: "Gelir vergisi", cell: (r) => money(r.income_tax), num: true },
                { label: "Net", cell: (r) => money(r.net), num: true },
                { label: "İşveren maliyeti", cell: (r) => money(r.employer_cost), num: true },
              ]}
            />
          </>
        )}
      </Async>
    </Card>
  );
}

function EmployerCost() {
  const [year, setYear] = useState(2025);
  const q = useQuery({
    queryKey: ["rep-cost", year],
    queryFn: () => api.get<{ items: { period: string; status: string; headcount: number; gross: string; net: string; employer_cost: string }[]; total: { gross: string; net: string; employer_cost: string } }>(`/reports/employer-cost?year=${year}`),
  });
  return (
    <Card title="Aylık işveren maliyeti" actions={<input type="number" min={2025} max={2026} value={year} onChange={(e) => setYear(Number(e.target.value))} aria-label="Yıl" />}>
      <Async q={q} isEmpty={(d) => d.items.length === 0} empty="Bu yıl hesaplanmış bordro yok.">
        {(d) => {
          const max = Math.max(...d.items.map((i) => Number(i.employer_cost)));
          return (
            <Table
              rows={d.items}
              rowKey={(r) => r.period}
              cols={[
                { label: "Dönem", cell: (r) => <>{monthName(r.period)} <Badge status={r.status} /></> },
                { label: "Kişi", cell: (r) => r.headcount, num: true },
                { label: "Brüt", cell: (r) => money(r.gross), num: true },
                { label: "Net", cell: (r) => money(r.net), num: true },
                { label: "İşveren maliyeti", cell: (r) => money(r.employer_cost), num: true },
                { label: "", cell: (r) => <span className="bar" style={{ width: `${(Number(r.employer_cost) / max) * 100}%` }} aria-hidden /> },
              ]}
            />
          );
        }}
      </Async>
    </Card>
  );
}

function LeaveUsage() {
  const [level, setLevel] = useState(2);
  const q = useQuery({
    queryKey: ["rep-leave", level],
    queryFn: () => api.get<{ as_of: string; items: { unit_path: string; unit_name: string; headcount: number; accrued_days: number; used_days: number; refunded_days: number; balance_days: number }[] }>(`/reports/leave-usage?level=${level}`),
  });
  return (
    <Card title="İzin kullanımı (aktif çalışanlar)" actions={<LevelSelect value={level} onChange={setLevel} />}>
      <Async q={q} isEmpty={(d) => d.items.length === 0}>
        {(d) => (
          <Table
            rows={d.items}
            rowKey={(r) => r.unit_path}
            cols={[
              { label: "Birim", cell: (r) => r.unit_name || r.unit_path },
              { label: "Kişi", cell: (r) => r.headcount, num: true },
              { label: "Hakedilen", cell: (r) => days(r.accrued_days), num: true },
              { label: "Kullanılan", cell: (r) => days(r.used_days), num: true },
              { label: "İade", cell: (r) => days(r.refunded_days), num: true },
              { label: "Kalan", cell: (r) => days(r.balance_days), num: true },
            ]}
          />
        )}
      </Async>
    </Card>
  );
}

function Overtime() {
  const [m, setM] = useState(addMonths(currentMonth(), -1));
  const q = useQuery({
    queryKey: ["rep-ot", m],
    queryFn: () => api.get<{ items: { unit_path: string; unit_name: string; approved_timesheets: number; overtime_minutes: number; rest_day_minutes: number; holiday_minutes: number }[]; over_annual_cap: { employee_no: string; year_overtime_minutes: number }[] }>(`/reports/overtime?month=${m}`),
  });
  return (
    <Card title="Fazla mesai (onaylı puantajlar)" actions={<input type="month" value={m} onChange={(e) => setM(e.target.value)} aria-label="Ay" />}>
      <Async q={q} isEmpty={(d) => d.items.length === 0} empty="Bu ay için onaylanmış puantaj yok.">
        {(d) => (
          <>
            <Table
              rows={d.items}
              rowKey={(r) => r.unit_path}
              cols={[
                { label: "Birim", cell: (r) => r.unit_name },
                { label: "Onaylı puantaj", cell: (r) => r.approved_timesheets, num: true },
                { label: "Fazla mesai", cell: (r) => hours(r.overtime_minutes), num: true },
                { label: "Hafta tatili", cell: (r) => hours(r.rest_day_minutes), num: true },
                { label: "Resmî tatil", cell: (r) => hours(r.holiday_minutes), num: true },
              ]}
            />
            {d.over_annual_cap.length > 0 && <p className="warn">270 saat yıllık sınırı aşan: {d.over_annual_cap.map((o) => o.employee_no).join(", ")}</p>}
          </>
        )}
      </Async>
    </Card>
  );
}
