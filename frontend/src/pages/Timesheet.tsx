import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { useAuth } from "../auth";
import { addMonths, currentMonth, date, dateTime, hours, month as monthName, time } from "../format";
import { EmployeePicker } from "../EmployeePicker";
import { Async, Badge, Card, Empty, FormError, PageHeader, ReasonButton, Stat, Table, Tabs } from "../ui";
import type { Correction, Days, MonthSummary, Page } from "../types";

const kindNames: Record<string, string> = {
  drop_duplicate: "Çift basım: ikincisi yok sayılsın",
  add_missing_out: "Eksik çıkış eklensin",
  add_missing_in: "Eksik giriş eklensin",
};
const segNames: Record<string, string> = { normal: "normal", rest_day: "hafta tatili", holiday: "resmî tatil" };

export default function TimesheetPage() {
  const { me, can } = useAuth();
  const [params, setParams] = useSearchParams();
  const team = can("timesheet.read.team") || can("timesheet.read.all");
  const decide = can("timesheet.approve.team") || can("timesheet.manage");
  const tabs = [
    ...(me?.employee_id ? [{ id: "mine", label: "Puantajım" }] : []),
    ...(team ? [{ id: "team", label: can("timesheet.read.all") ? "Çalışan puantajı" : "Ekip puantajı" }] : []),
    ...(decide ? [{ id: "corrections", label: "Düzeltme önerileri" }] : []),
  ];
  const tab = params.get("tab") && tabs.some((t) => t.id === params.get("tab")) ? params.get("tab")! : tabs[0]?.id;
  const [picked, setPicked] = useState<number | null>(null);

  return (
    <>
      <PageHeader
        title="Puantaj"
        sub="Basımlar hiçbir zaman değiştirilmez; tutarsız basımlar için sistem düzeltme önerir, onaylanmadan hiçbir şey değişmez."
      />
      <Tabs tabs={tabs} value={tab} onChange={(t) => setParams({ tab: t })} />
      {tab === "mine" && (
        <>
          <Clock />
          <MonthView employeeId={me!.employee_id!} />
        </>
      )}
      {tab === "team" && (
        <>
          <Card>
            <EmployeePicker value={picked} onChange={(id) => setPicked(id)} />
          </Card>
          {picked ? <MonthView employeeId={picked} canApprove={decide && picked !== me?.employee_id} /> : <Empty>Puantajını görmek için bir çalışan seçin.</Empty>}
        </>
      )}
      {tab === "corrections" && <CorrectionQueue />}
    </>
  );
}

function Clock() {
  const qc = useQueryClient();
  const m = useMutation({
    mutationFn: (direction: "in" | "out") => api.post<{ punched_at: string; direction: string }>("/timesheet/clock", { direction }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ts-days"] }),
  });
  return (
    <Card title="Web saati">
      <div className="row-actions">
        <button className="btn primary" disabled={m.isPending} onClick={() => m.mutate("in")}>Giriş bas</button>
        <button className="btn" disabled={m.isPending} onClick={() => m.mutate("out")}>Çıkış bas</button>
        {m.isSuccess && <span className="ok">{m.data.direction === "in" ? "Giriş" : "Çıkış"} kaydedildi: {time(m.data.punched_at)}</span>}
      </div>
      <FormError error={m.error} />
    </Card>
  );
}

function MonthView({ employeeId, canApprove }: { employeeId: number; canApprove?: boolean }) {
  const qc = useQueryClient();
  const [m, setM] = useState(addMonths(currentMonth(), -1));
  const summary = useQuery({
    queryKey: ["ts-month", employeeId, m],
    queryFn: () => api.get<MonthSummary>(`/timesheet/employees/${employeeId}/months/${m}`),
  });
  const days = useQuery({
    queryKey: ["ts-days", employeeId, summary.data?.from, summary.data?.to],
    queryFn: () => api.get<Days>(`/timesheet/employees/${employeeId}/days?from=${summary.data!.from}&to=${summary.data!.to}`),
    enabled: !!summary.data,
  });
  const approve = useMutation({
    mutationFn: () => api.post(`/timesheet/employees/${employeeId}/months/${m}/approve`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ts-month", employeeId, m] }),
  });

  return (
    <div className="grid">
      <Card
        title={`${monthName(m)} bordro ayı`}
        actions={
          <div className="row-actions">
            <button className="btn small ghost" onClick={() => setM(addMonths(m, -1))} aria-label="Önceki ay">←</button>
            <button className="btn small ghost" onClick={() => setM(addMonths(m, 1))} aria-label="Sonraki ay" disabled={m >= currentMonth()}>→</button>
          </div>
        }
      >
        <Async q={summary}>
          {(s) => (
            <>
              <p className="muted small">
                {date(s.from)} – {date(s.to)} (Pazar günü bu aya düşen haftalar) · <Badge status={s.status} /> · kural seti {s.rule_version ?? ""}
              </p>
              <div className="stats">
                <Stat label="Normal" value={hours(s.totals.normal_minutes)} />
                <Stat label={`Fazla mesai (%${s.multipliers_pct.overtime})`} value={hours(s.totals.overtime_minutes)} hint="haftalık 45 saat üstü" />
                <Stat label={`Hafta tatili (%${s.multipliers_pct.rest_day})`} value={hours(s.totals.rest_day_minutes)} />
                <Stat label={`Resmî tatil (%${s.multipliers_pct.holiday})`} value={hours(s.totals.holiday_minutes)} />
                <Stat label="Çalışılan gün" value={s.totals.worked_days} />
                <Stat label="Yıllık fazla mesai" value={hours(s.year_overtime_minutes)} hint={s.overtime_cap_exceeded ? "⚠ 270 saat sınırı aşıldı" : "sınır 270 saat"} />
              </div>
              {s.open_anomalies > 0 && <p className="warn">{s.open_anomalies} tutarsız basım düzeltme onayı bekliyor; bu süreler toplamlara girmez ve ay onaylanamaz.</p>}
              {canApprove && s.status === "open" && (
                <div className="row-actions">
                  <button className="btn primary" disabled={approve.isPending || s.open_anomalies > 0} onClick={() => approve.mutate()}>Ayı onayla</button>
                  <FormError error={approve.error} />
                </div>
              )}
              {s.weeks.length > 0 && (
                <Table
                  rows={s.weeks}
                  rowKey={(w) => w.week_start}
                  cols={[
                    { label: "Hafta", cell: (w) => `${date(w.week_start)} – ${date(w.week_end)}` },
                    { label: "Normal", cell: (w) => hours(w.normal_minutes), num: true },
                    { label: "Fazla mesai", cell: (w) => hours(w.overtime_minutes), num: true },
                    { label: "H. tatili", cell: (w) => hours(w.rest_day_minutes), num: true },
                    { label: "R. tatil", cell: (w) => hours(w.holiday_minutes), num: true },
                  ]}
                />
              )}
            </>
          )}
        </Async>
      </Card>
      <Card title="Günler">
        {!summary.data ? null : (
          <Async q={days} isEmpty={(d) => d.days.length === 0 && d.anomalies.length === 0} empty="Bu dönemde basım yok.">
            {(d) => (
              <>
                {d.anomalies.length > 0 && (
                  <div className="anomalies">
                    <h3>Tutarsız basımlar</h3>
                    <ul className="plain">
                      {d.anomalies.map((a) => (
                        <li key={a.suggestion_key}>
                          <Badge status="pending" /> {dateTime(a.punch_at)}: {a.reason}
                          {a.suggested_at && <span className="muted"> · öneri: {dateTime(a.suggested_at)}</span>}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
                <Table
                  rows={d.days}
                  rowKey={(x) => x.date}
                  cols={[
                    { label: "Gün", cell: (x) => date(x.date) },
                    { label: "Giriş–Çıkış", cell: (x) => x.sessions.map((s) => `${time(s.in)}–${time(s.out)}`).join(", ") },
                    { label: "Mola", cell: (x) => hours(x.sessions.reduce((a, s) => a + s.break_minutes, 0)), num: true },
                    { label: "Net", cell: (x) => hours(x.net_minutes), num: true },
                    {
                      label: "Dağılım",
                      cell: (x) =>
                        x.sessions.flatMap((s) => s.segments).filter((g) => g.minutes > 0).map((g) => `${date(g.date)} ${segNames[g.kind]} ${hours(g.minutes)}`).join(" · "),
                    },
                  ]}
                />
              </>
            )}
          </Async>
        )}
      </Card>
    </div>
  );
}

function CorrectionQueue() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["ts-corrections"],
    queryFn: () => api.get<Page<Correction>>("/timesheet/corrections?for_approval=true&limit=100"),
  });
  const act = useMutation({
    mutationFn: ({ id, action, note }: { id: number; action: string; note?: string }) =>
      api.post(`/timesheet/corrections/${id}/${action}`, note ? { note } : undefined),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ts-corrections"] }),
  });
  return (
    <Card title="Onayınızı bekleyen düzeltme önerileri">
      <FormError error={act.error} />
      <Async q={q} isEmpty={(d) => d.items.length === 0} empty="Bekleyen öneri yok.">
        {(d) => (
          <Table
            rows={d.items}
            rowKey={(c) => c.id}
            cols={[
              { label: "Çalışan", cell: (c) => c.employee_name },
              { label: "Gün", cell: (c) => date(c.work_date) },
              { label: "Basım", cell: (c) => `${c.target_direction === "in" ? "Giriş" : "Çıkış"} ${dateTime(c.target_punch_at)}` },
              { label: "Öneri", cell: (c) => kindNames[c.kind] + (c.proposed_at ? ` (${time(c.proposed_at)})` : "") },
              { label: "Neden", cell: (c) => <span className="muted small">{c.reason}</span> },
              {
                label: "İşlem",
                cell: (c) => (
                  <div className="row-actions">
                    <button className="btn small primary" disabled={act.isPending} onClick={() => act.mutate({ id: c.id, action: "approve" })}>Onayla</button>
                    <ReasonButton label="Reddet" busy={act.isPending} onSubmit={(note) => act.mutate({ id: c.id, action: "reject", note })} />
                  </div>
                ),
              },
            ]}
          />
        )}
      </Async>
    </Card>
  );
}
