import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { api, newKey, openAuthed } from "../api/client";
import { useAuth } from "../auth";
import { addMonths, currentMonth, dateTime, money, month as monthName } from "../format";
import { EmployeePicker } from "../EmployeePicker";
import { Async, Badge, Card, Empty, ErrorBox, FormError, PageHeader, ReasonButton, Table, Tabs } from "../ui";
import type { PayResult, PaySummary, Period, PayrollItem, RunReport } from "../types";

export default function PayrollPage() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const tabs = [
    { id: "periods", label: "Dönemler" },
    { id: "employee", label: "Çalışan bordrosu" },
    ...(can("payroll.items.manage") ? [{ id: "items", label: "Avans / borç / prim" }] : []),
  ];
  const tab = params.get("tab") && tabs.some((t) => t.id === params.get("tab")) ? params.get("tab")! : "periods";
  return (
    <>
      <PageHeader title="Bordro" sub="Taslak → kontrol → onay → kapalı. Hesaplayan onaylayamaz; kapanan dönem veritabanında dondurulur." />
      <Tabs tabs={tabs} value={tab} onChange={(t) => setParams({ tab: t })} />
      {tab === "periods" && <Periods />}
      {tab === "employee" && <EmployeePayroll />}
      {tab === "items" && <Items />}
    </>
  );
}

function Periods() {
  const { can } = useAuth();
  const qc = useQueryClient();
  const periods = useQuery({ queryKey: ["periods"], queryFn: () => api.get<{ items: Period[] }>("/payroll/periods") });
  const [runMonth, setRunMonth] = useState("2025-01");
  const [report, setReport] = useState<RunReport | null>(null);
  const run = useMutation({
    mutationFn: (p: string) => api.post<RunReport>(`/payroll/periods/${p}/run`),
    onSuccess: (r) => {
      setReport(r);
      qc.invalidateQueries({ queryKey: ["periods"] });
    },
  });
  const step = useMutation({
    mutationFn: ({ p, action, note }: { p: string; action: string; note?: string }) =>
      api.post(`/payroll/periods/${p}/${action}`, note ? { note } : undefined),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["periods"] }),
  });
  const busy = run.isPending || step.isPending;

  // Stacked, not in the two-column grid: the periods table needs the full width.
  return (
    <div>
      {can("payroll.calculate") && (
        <Card title="Dönem hesapla">
          <form className="inline-form" onSubmit={(e) => { e.preventDefault(); setReport(null); run.mutate(runMonth); }}>
            <input type="month" required value={runMonth} onChange={(e) => setRunMonth(e.target.value)} aria-label="Dönem" />
            <button className="btn primary" disabled={busy}>{run.isPending ? "Hesaplanıyor…" : "Tüm çalışanları hesapla"}</button>
          </form>
          <p className="muted small">Önceki ay açılmışsa önce kapatılmalıdır (kümülatif matrah ondan gelir).</p>
          <FormError error={run.error} />
          {report && (
            <div className={report.failures.length ? "warn" : "ok-box"}>
              {report.employees} çalışan, {report.duration_ms} ms ({report.workers} paralel işçi) · {report.changed} değişti, {report.unchanged} aynı ·
              özet <code>{report.digest.slice(0, 12)}</code>
              {report.failures.length > 0 && <> · {report.failures.length} hata (ilk: #{report.failures[0].employee_id} {report.failures[0].error})</>}
            </div>
          )}
        </Card>
      )}
      <Card title="Dönemler">
        <FormError error={step.error} />
        <Async q={periods} isEmpty={(d) => d.items.length === 0} empty="Henüz dönem yok. Bir ayı hesaplayarak başlayın.">
          {(d) => (
            <Table
              rows={d.items}
              rowKey={(p) => p.period}
              cols={[
                { label: "Dönem", cell: (p) => monthName(p.period) },
                { label: "Durum", cell: (p) => <><Badge status={p.status} />{p.review_note && <span className="muted small"> {p.review_note}</span>}</> },
                { label: "Kişi", cell: (p) => p.employee_count, num: true },
                { label: "Net toplam", cell: (p) => money(p.totals.net), num: true },
                { label: "İşveren maliyeti", cell: (p) => money(p.totals.employer_cost), num: true },
                { label: "Son hesap", cell: (p) => `${dateTime(p.last_run_at)}${p.last_run_ms != null ? ` · ${p.last_run_ms} ms` : ""}` },
                {
                  label: "İşlem",
                  cell: (p) => (
                    <div className="row-actions">
                      {p.status === "draft" && can("payroll.calculate") && (
                        <>
                          <button className="btn small" disabled={busy} onClick={() => { setReport(null); run.mutate(p.period); }}>Yeniden hesapla</button>
                          <button className="btn small primary" disabled={busy} onClick={() => step.mutate({ p: p.period, action: "submit" })}>Kontrole gönder</button>
                        </>
                      )}
                      {p.status === "review" && can("payroll.approve") && (
                        <>
                          <button className="btn small primary" disabled={busy} onClick={() => step.mutate({ p: p.period, action: "approve" })}>Onayla</button>
                          <ReasonButton label="Reddet" busy={busy} onSubmit={(note) => step.mutate({ p: p.period, action: "reject", note })} />
                        </>
                      )}
                      {p.status === "approved" && can("payroll.close") && (
                        <button className="btn small primary" disabled={busy} onClick={() => step.mutate({ p: p.period, action: "close" })}>Dönemi kapat</button>
                      )}
                      {can("reports.read") && (
                        <button className="btn small ghost" onClick={() => openAuthed(`/reports/payroll-summary?period=${p.period}&format=csv`, `bordro-ozet-${p.period}.csv`)}>CSV</button>
                      )}
                    </div>
                  ),
                },
              ]}
            />
          )}
        </Async>
      </Card>
    </div>
  );
}

function EmployeePayroll() {
  const { can } = useAuth();
  const qc = useQueryClient();
  const [emp, setEmp] = useState<number | null>(null);
  const [year, setYear] = useState(Number(currentMonth().slice(0, 4)));
  const [period, setPeriod] = useState<string | null>(null);
  const [calcMonth, setCalcMonth] = useState(addMonths(currentMonth(), -1));
  const results = useQuery({
    queryKey: ["pay-results", emp, year],
    queryFn: () => api.get<{ items: PaySummary[] }>(`/payroll/employees/${emp}/results?year=${year}`),
    enabled: !!emp,
  });
  const calc = useMutation({
    mutationFn: (p: string) => api.post<PayResult>(`/payroll/employees/${emp}/calculate`, { period: p }),
    onSuccess: (_, p) => {
      setPeriod(p);
      qc.invalidateQueries({ queryKey: ["pay-results", emp] });
      qc.invalidateQueries({ queryKey: ["pay-result", emp, p] });
    },
  });
  return (
    <div className="grid">
      <Card>
        <div className="form-grid">
          <EmployeePicker value={emp} onChange={(id) => { setEmp(id); setPeriod(null); }} />
          <label>Yıl<input type="number" min={2025} max={2026} value={year} onChange={(e) => setYear(Number(e.target.value))} /></label>
          {can("payroll.calculate") && emp && (
            <label>
              Tek ay hesapla (taslak dönem)
              <span className="inline-form">
                <input type="month" value={calcMonth} onChange={(e) => setCalcMonth(e.target.value)} />
                <button type="button" className="btn" disabled={calc.isPending} onClick={() => calc.mutate(calcMonth)}>Hesapla</button>
              </span>
            </label>
          )}
        </div>
        <FormError error={calc.error} />
      </Card>
      {!emp ? (
        <Empty>Bordrosunu görmek için bir çalışan seçin.</Empty>
      ) : (
        <Card title={`${year} bordroları`}>
          <Async q={results} isEmpty={(d) => d.items.length === 0} empty="Bu yıl için hesaplanmış bordro yok.">
            {(d) => (
              <Table
                rows={d.items}
                rowKey={(r) => r.period}
                onRow={(r) => setPeriod(r.period)}
                cols={[
                  { label: "Dönem", cell: (r) => monthName(r.period) },
                  { label: "Brüt", cell: (r) => money(r.gross), num: true },
                  { label: "Gelir vergisi", cell: (r) => money(r.income_tax), num: true },
                  { label: "Kümülatif matrah", cell: (r) => money(r.cum_tax_base), num: true },
                  { label: "Net", cell: (r) => money(r.net), num: true },
                  { label: "İşveren maliyeti", cell: (r) => money(r.employer_cost), num: true },
                ]}
              />
            )}
          </Async>
        </Card>
      )}
      {emp && period && <PayslipLines employeeId={emp} period={period} />}
    </div>
  );
}

/** One month's explained lines; shared with the employee's own payslip view. */
export function PayslipLines({ employeeId, period }: { employeeId: number; period: string }) {
  const r = useQuery({
    queryKey: ["pay-result", employeeId, period],
    queryFn: () => api.get<{ result: PayResult; inputs: { context: { notes: string[] | null; timesheet_status: string } } }>(`/payroll/employees/${employeeId}/results/${period}`),
  });
  const [err, setErr] = useState<unknown>(null);
  return (
    <Card
      title={`${monthName(period)} bordrosu`}
      actions={
        <button className="btn small" onClick={() => openAuthed(`/payroll/employees/${employeeId}/payslips/${period}?format=html`).catch(setErr)}>
          Yazdır / PDF
        </button>
      }
    >
      {err != null && <ErrorBox error={err} />}
      <Async q={r}>
        {(d) => (
          <>
            <p className="muted small">Kural seti {d.result.rule_version} · puantaj: {d.inputs.context.timesheet_status === "approved" ? "onaylı" : "onaylanmamış"}</p>
            <Table
              rows={d.result.lines}
              rowKey={(l) => l.code}
              cols={[
                { label: "Kalem", cell: (l) => (["gross", "net", "employer_cost"].includes(l.code) ? <strong>{l.label}</strong> : l.label) },
                { label: "Hesap", cell: (l) => <span className="muted small">{l.formula}</span> },
                { label: "Tutar", cell: (l) => money(l.amount), num: true },
              ]}
            />
            {d.inputs.context.notes?.map((n) => <p key={n} className="muted small">Not: {n}</p>)}
          </>
        )}
      </Async>
    </Card>
  );
}

const kindNames: Record<string, string> = { bonus: "Prim / ek ödeme", advance: "Avans", loan: "Borç (taksitli)" };

function Items() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["pay-items"], queryFn: () => api.get<{ items: PayrollItem[] }>("/payroll/items") });
  const [key, setKey] = useState(newKey);
  const [emp, setEmp] = useState<number | null>(null);
  const [f, setF] = useState({ kind: "loan", label: "", amount: "", instalments: "3", start_period: currentMonth() });
  const create = useMutation({
    mutationFn: () =>
      api.post("/payroll/items", { employee_id: emp, ...f, amount: f.amount.replace(",", "."), instalments: f.kind === "loan" ? Number(f.instalments) : 1 }, { idempotencyKey: key }),
    onSuccess: () => {
      setKey(newKey());
      setF({ ...f, label: "", amount: "" });
      qc.invalidateQueries({ queryKey: ["pay-items"] });
    },
  });
  const cancel = useMutation({
    mutationFn: (id: number) => api.post(`/payroll/items/${id}/cancel`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["pay-items"] }),
  });
  return (
    <div className="grid">
      <Card title="Yeni kalem">
        <form className="form-grid" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
          <EmployeePicker value={emp} onChange={setEmp} />
          <label>
            Tür
            <select value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}>
              {Object.entries(kindNames).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
          <label>Açıklama<input required maxLength={100} value={f.label} onChange={(e) => setF({ ...f, label: e.target.value })} /></label>
          <label>Tutar (TL)<input required inputMode="decimal" placeholder="9000.00" value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} /></label>
          {f.kind === "loan" && <label>Taksit<input type="number" min={1} max={36} value={f.instalments} onChange={(e) => setF({ ...f, instalments: e.target.value })} /></label>}
          <label>Başlangıç dönemi<input type="month" required value={f.start_period} onChange={(e) => setF({ ...f, start_period: e.target.value })} /></label>
          <FormError error={create.error} />
          <button className="btn primary" disabled={create.isPending || !emp}>Ekle</button>
        </form>
        <p className="muted small">Bir aylık kesinti brüt maaşın dörtte birini aşamaz. Kontrol, onay ya da kapalı döneme değen kalem eklenemez.</p>
      </Card>
      <Card title="Kalemler">
        <FormError error={cancel.error} />
        <Async q={list} isEmpty={(d) => d.items.length === 0} empty="Kalem yok.">
          {(d) => (
            <Table
              rows={d.items}
              rowKey={(i) => i.id}
              cols={[
                { label: "Çalışan #", cell: (i) => i.employee_id },
                { label: "Tür", cell: (i) => kindNames[i.kind] },
                { label: "Açıklama", cell: (i) => i.label },
                { label: "Tutar", cell: (i) => money(i.amount), num: true },
                { label: "Takvim", cell: (i) => i.schedule.map((s) => `${s.period}: ${money(s.amount)}`).join(" · ") },
                { label: "Durum", cell: (i) => (i.cancelled_at ? <Badge status="cancelled" /> : <Badge status="approved" />) },
                { label: "İşlem", cell: (i) => (!i.cancelled_at ? <button className="btn small ghost" onClick={() => cancel.mutate(i.id)}>İptal</button> : null) },
              ]}
            />
          )}
        </Async>
      </Card>
    </div>
  );
}
