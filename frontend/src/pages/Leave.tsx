import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { api, newKey } from "../api/client";
import { useAuth } from "../auth";
import { date, days, leaveTypes, statusNames, todayIso } from "../format";
import { EmployeePicker } from "../EmployeePicker";
import { Async, Badge, Card, FormError, PageHeader, ReasonButton, Stat, Table, Tabs } from "../ui";
import type { Balance, Delegation, LeaveRequest, LedgerRow, Page } from "../types";

export default function LeavePage() {
  const { me, can } = useAuth();
  const [params, setParams] = useSearchParams();
  const tabs = [
    ...(me?.employee_id ? [{ id: "mine", label: "İzinlerim" }] : []),
    ...(can("leave.approve.team") || can("leave.manage") ? [{ id: "approvals", label: "Onay bekleyenler" }] : []),
    ...(can("leave.read.team") || can("leave.read.all") ? [{ id: "team", label: can("leave.read.all") ? "Tüm talepler" : "Ekip talepleri" }] : []),
    ...(me?.employee_id ? [{ id: "delegations", label: "Vekâlet" }] : []),
  ];
  const tab = params.get("tab") && tabs.some((t) => t.id === params.get("tab")) ? params.get("tab")! : tabs[0]?.id;
  return (
    <>
      <PageHeader title="İzinler" sub="Yıllık izin hakedişi 4857 s. İş Kanunu md. 53'e göre hesaplanır; hafta sonu ve resmî tatiller düşülmez." />
      <Tabs tabs={tabs} value={tab} onChange={(t) => setParams({ tab: t })} />
      {tab === "mine" && <MyLeave />}
      {tab === "approvals" && <RequestList query="for_approval=true" decide />}
      {tab === "team" && <RequestList query="" />}
      {tab === "delegations" && <Delegations />}
    </>
  );
}

function MyLeave() {
  const { me } = useAuth();
  const id = me!.employee_id!;
  const balance = useQuery({ queryKey: ["balance", id], queryFn: () => api.get<Balance>(`/leave/employees/${id}/balance`) });
  const ledger = useQuery({ queryKey: ["ledger", id], queryFn: () => api.get<{ items: LedgerRow[] }>(`/leave/employees/${id}/ledger`) });
  return (
    <div className="grid">
      <Card title="Bakiye">
        <Async q={balance}>
          {(b) => (
            <div className="stats">
              <Stat label="Kullanılabilir" value={days(b.available)} hint="bakiye − bekleyen talepler" />
              <Stat label="Bakiye" value={days(b.balance)} />
              <Stat label="Devreden" value={days(b.carried_over)} hint="son hakedişten önceki bakiye" />
              <Stat label="Bu yılın hakedişi" value={days(b.current_accrual)} />
              <Stat label="Kıdem" value={`${b.service_years} yıl`} hint={`İşe giriş ${date(b.hire_date)}`} />
              <Stat label="Sonraki hakediş" value={b.next_accrual ? days(b.next_accrual.days) : "—"} hint={date(b.next_accrual?.date)} />
            </div>
          )}
        </Async>
      </Card>
      <NewRequest employeeId={id} />
      <RequestList query={`employee_id=${id}`} own />
      <Card title="Hareketler (defter)">
        <Async q={ledger} isEmpty={(d) => d.items.length === 0} empty="Henüz hareket yok.">
          {(d) => (
            <Table
              rows={[...d.items].reverse()}
              rowKey={(r) => r.id}
              cols={[
                { label: "Tarih", cell: (r) => date(r.effective_date) },
                { label: "Hareket", cell: (r) => r.note || r.kind },
                { label: "Gün", cell: (r) => (r.days > 0 ? "+" : "") + r.days.toLocaleString("tr-TR"), num: true },
                { label: "Bakiye", cell: (r) => r.running_balance.toLocaleString("tr-TR"), num: true },
              ]}
            />
          )}
        </Async>
      </Card>
    </div>
  );
}

function NewRequest({ employeeId }: { employeeId: number }) {
  const qc = useQueryClient();
  const [key, setKey] = useState(newKey);
  const [f, setF] = useState({ leave_type: "annual", start_date: "", end_date: "", half_day: "", reason: "" });
  const m = useMutation({
    mutationFn: () =>
      api.post<LeaveRequest>("/leave/requests", { ...f, end_date: f.half_day ? f.start_date : f.end_date }, { idempotencyKey: key }),
    onSuccess: () => {
      setF({ ...f, start_date: "", end_date: "", half_day: "", reason: "" });
      setKey(newKey());
      qc.invalidateQueries({ queryKey: ["leave"] });
      qc.invalidateQueries({ queryKey: ["balance", employeeId] });
    },
  });
  return (
    <Card title="Yeni talep">
      <form className="form-grid" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        <label>
          Tür
          <select value={f.leave_type} onChange={(e) => setF({ ...f, leave_type: e.target.value })}>
            {Object.entries(leaveTypes).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </label>
        <label>Başlangıç<input type="date" required min={f.leave_type === "sick" ? undefined : todayIso()} value={f.start_date} onChange={(e) => setF({ ...f, start_date: e.target.value })} /></label>
        <label>
          Yarım gün
          <select value={f.half_day} onChange={(e) => setF({ ...f, half_day: e.target.value })}>
            <option value="">Hayır (tam gün)</option>
            <option value="morning">Sabah</option>
            <option value="afternoon">Öğleden sonra</option>
          </select>
        </label>
        {!f.half_day && <label>Bitiş<input type="date" required min={f.start_date} value={f.end_date} onChange={(e) => setF({ ...f, end_date: e.target.value })} /></label>}
        <label className="wide">Açıklama<input maxLength={500} value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></label>
        <FormError error={m.error} />
        {m.isSuccess && <p className="ok">Talep oluşturuldu: {days(m.data.days)} iş günü.</p>}
        <button className="btn primary" disabled={m.isPending}>{m.isPending ? "Gönderiliyor…" : "Talep gönder"}</button>
      </form>
    </Card>
  );
}

function RequestList({ query, decide, own }: { query: string; decide?: boolean; own?: boolean }) {
  const qc = useQueryClient();
  const manage = useAuth().can("leave.manage") && !own;
  const [status, setStatus] = useState("");
  const q = useQuery({
    queryKey: ["leave", query, status],
    queryFn: () => api.get<Page<LeaveRequest>>(`/leave/requests?limit=100&${query}${status ? "&status=" + status : ""}`),
  });
  const act = useMutation({
    mutationFn: ({ id, action, note }: { id: number; action: string; note?: string }) =>
      api.post(`/leave/requests/${id}/${action}`, note ? { note } : undefined, action === "approve" ? { idempotencyKey: newKey() } : undefined),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["leave"] });
      qc.invalidateQueries({ queryKey: ["balance"] });
    },
  });
  return (
    <Card
      title={decide ? "Onayınızı bekleyen talepler" : own ? "Taleplerim" : "Talepler"}
      actions={
        !decide && (
          <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Durum">
            <option value="">Tümü</option>
            {["pending", "approved", "rejected", "cancelled"].map((s) => <option key={s} value={s}>{statusNames[s]}</option>)}
          </select>
        )
      }
    >
      <FormError error={act.error} />
      <Async q={q} isEmpty={(d) => d.items.length === 0} empty={decide ? "Onay bekleyen talep yok." : "Talep yok."}>
        {(d) => (
          <Table
            rows={d.items}
            rowKey={(r) => r.id}
            cols={[
              ...(own ? [] : [{ label: "Çalışan", cell: (r: LeaveRequest) => r.employee_name }]),
              { label: "Tür", cell: (r) => leaveTypes[r.leave_type] + (r.half_day ? ` (${r.half_day === "morning" ? "sabah" : "öğleden sonra"})` : "") },
              { label: "Tarih", cell: (r) => (r.start_date === r.end_date ? date(r.start_date) : `${date(r.start_date)} – ${date(r.end_date)}`) },
              { label: "İş günü", cell: (r) => r.days.toLocaleString("tr-TR"), num: true },
              { label: "Durum", cell: (r) => <><Badge status={r.status} />{r.decision_note && <span className="muted small"> {r.decision_note}</span>}</> },
              {
                label: "İşlem",
                cell: (r) =>
                  r.status === "pending" && decide ? (
                    <div className="row-actions">
                      <button className="btn small primary" disabled={act.isPending} onClick={() => act.mutate({ id: r.id, action: "approve" })}>Onayla</button>
                      <ReasonButton label="Reddet" busy={act.isPending} onSubmit={(note) => act.mutate({ id: r.id, action: "reject", note })} />
                    </div>
                  ) : r.status === "pending" && own ? (
                    <button className="btn small ghost" onClick={() => act.mutate({ id: r.id, action: "cancel" })}>Geri çek</button>
                  ) : manage && (r.status === "pending" || (r.status === "approved" && r.leave_type !== "sick")) ? (
                    <button className="btn small ghost" title="Onaylı izinde düşülen günler bakiyeye iade edilir" onClick={() => act.mutate({ id: r.id, action: "cancel" })}>İptal et</button>
                  ) : null,
              },
            ]}
          />
        )}
      </Async>
    </Card>
  );
}

function Delegations() {
  const { me, can } = useAuth();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["delegations"], queryFn: () => api.get<{ items: Delegation[] }>("/delegations") });
  const [f, setF] = useState({ delegate_employee_id: "", start_date: todayIso(), end_date: todayIso(), reason: "" });
  const create = useMutation({
    mutationFn: () => api.post("/delegations", { ...f, delegate_employee_id: Number(f.delegate_employee_id) }, { idempotencyKey: newKey() }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["delegations"] }),
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.post(`/delegations/${id}/revoke`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["delegations"] }),
  });
  const canDelegate = can("delegation.manage.self") || can("delegation.manage.all");
  return (
    <div className="grid">
      {canDelegate && (
        <Card title="Vekâlet ver">
          <p className="muted small">Vekil, süre boyunca yalnızca ekip izinlerini alır (ekibi görme, izin ve puantaj onayı); şirket geneli yetkiler devredilmez.</p>
          <form className="form-grid" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
            <EmployeePicker label="Vekil" value={f.delegate_employee_id ? Number(f.delegate_employee_id) : null} onChange={(id) => setF({ ...f, delegate_employee_id: id ? String(id) : "" })} />
            <label>Başlangıç<input type="date" required value={f.start_date} onChange={(e) => setF({ ...f, start_date: e.target.value })} /></label>
            <label>Bitiş<input type="date" required value={f.end_date} onChange={(e) => setF({ ...f, end_date: e.target.value })} /></label>
            <label className="wide">Gerekçe<input value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></label>
            <FormError error={create.error} />
            <button className="btn primary" disabled={create.isPending}>Vekâlet ver</button>
          </form>
        </Card>
      )}
      <Card title="Vekâletler">
        <FormError error={revoke.error} />
        <Async q={list} isEmpty={(d) => d.items.length === 0} empty="Verdiğiniz ya da aldığınız vekâlet yok.">
          {(d) => (
            <Table
              rows={d.items}
              rowKey={(r) => r.id}
              cols={[
                { label: "Yönetici", cell: (r) => r.manager_name },
                { label: "Vekil", cell: (r) => r.delegate_name },
                { label: "Süre", cell: (r) => `${date(r.start_date)} – ${date(r.end_date)}` },
                { label: "Durum", cell: (r) => <Badge status={r.revoked_at ? "cancelled" : r.active ? "approved" : "pending"} /> },
                {
                  label: "İşlem",
                  cell: (r) =>
                    !r.revoked_at && (can("delegation.manage.all") || r.manager_employee_id === me?.employee_id) ? (
                      <button className="btn small ghost" onClick={() => revoke.mutate(r.id)}>İptal et</button>
                    ) : null,
                },
              ]}
            />
          )}
        </Async>
      </Card>
    </div>
  );
}
