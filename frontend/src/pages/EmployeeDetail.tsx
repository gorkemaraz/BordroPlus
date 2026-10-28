import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import { useAuth } from "../auth";
import { date, days, money } from "../format";
import { Async, Card, FormError, PageHeader, Stat, Table } from "../ui";
import type { Balance, Employee } from "../types";
import { useUnits } from "./Employees";

export default function EmployeeDetailPage() {
  const { id } = useParams();
  const { me, can } = useAuth();
  const self = me?.employee_id === Number(id);
  const emp = useQuery({ queryKey: ["employee", id], queryFn: () => api.get<Employee>(`/employees/${id}`) });
  const salaries = useQuery({
    queryKey: ["salaries", id],
    queryFn: () => api.get<{ items: { valid_from: string; amount: string }[] }>(`/employees/${id}/salaries`),
    enabled: can("salary.read") || self,
  });
  const leaveVisible = self || can("leave.read.team") || can("leave.read.all");
  const balance = useQuery({
    queryKey: ["balance", Number(id)],
    queryFn: () => api.get<Balance>(`/leave/employees/${id}/balance`),
    enabled: leaveVisible,
    retry: false,
  });

  return (
    <Async q={emp}>
      {(e) => (
        <>
          <PageHeader title={`${e.first_name} ${e.last_name}`} sub={`${e.employee_no} · ${e.title || "—"} · ${e.org_unit_name}`} actions={<Link to="/employees">← Personel</Link>} />
          <div className="grid">
            <Card title="Kart">
              <dl className="kv">
                <dt>Birim yolu</dt><dd><code>{e.org_unit_path}</code></dd>
                <dt>İşe giriş</dt><dd>{date(e.hire_date)}</dd>
                <dt>Ayrılış</dt><dd>{date(e.termination_date)}</dd>
                <dt>Güncel brüt maaş</dt>
                <dd>{e.salary_masked ? <span className="masked" title="Bu alanı görme yetkiniz yok">•••••• (maskeli)</span> : e.salary ? `${money(e.salary.amount)} (${date(e.salary.valid_from)}'den)` : "—"}</dd>
              </dl>
            </Card>
            {leaveVisible && (
              <Card title="Yıllık izin">
                <Async q={balance}>
                  {(b) => (
                    <div className="stats">
                      <Stat label="Kıdem" value={`${b.service_years} yıl`} />
                      <Stat label="Bakiye" value={days(b.balance)} />
                      <Stat label="Kullanılan" value={days(b.total_used)} />
                      <Stat label="Sonraki hakediş" value={b.next_accrual ? days(b.next_accrual.days) : "—"} hint={date(b.next_accrual?.date)} />
                    </div>
                  )}
                </Async>
              </Card>
            )}
            {(can("salary.read") || self) && (
              <Card title="Maaş geçmişi">
                <Async q={salaries} isEmpty={(d) => d.items.length === 0} empty="Maaş kaydı yok.">
                  {(d) => <Table rows={d.items} rowKey={(s) => s.valid_from} cols={[{ label: "Geçerlilik", cell: (s) => date(s.valid_from) }, { label: "Brüt", cell: (s) => money(s.amount), num: true }]} />}
                </Async>
                {can("salary.write") && <AddSalary id={e.id} />}
              </Card>
            )}
            {can("employee.write") && <EditEmployee e={e} />}
          </div>
        </>
      )}
    </Async>
  );
}

function AddSalary({ id }: { id: number }) {
  const qc = useQueryClient();
  const [amount, setAmount] = useState("");
  const [from, setFrom] = useState("");
  const m = useMutation({
    mutationFn: () => api.post(`/employees/${id}/salaries`, { amount: amount.replace(",", "."), valid_from: from }),
    onSuccess: () => {
      setAmount("");
      qc.invalidateQueries({ queryKey: ["salaries", String(id)] });
      qc.invalidateQueries({ queryKey: ["employee", String(id)] });
    },
  });
  return (
    <form className="inline-form" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
      <input inputMode="decimal" required placeholder="Yeni brüt (ör. 70000.00)" value={amount} onChange={(e) => setAmount(e.target.value)} />
      <input type="date" required value={from} onChange={(e) => setFrom(e.target.value)} aria-label="Geçerlilik" />
      <button className="btn" disabled={m.isPending}>Ekle</button>
      <FormError error={m.error} />
      <p className="muted small">Geçmiş tarihli maaş, kapanmış bordro aylarına sonraki bordroda "geçmiş dönem düzeltmesi" olarak yansır.</p>
    </form>
  );
}

function EditEmployee({ e }: { e: Employee }) {
  const qc = useQueryClient();
  const units = useUnits();
  const [title, setTitle] = useState(e.title);
  const [unit, setUnit] = useState(String(e.org_unit_id));
  const [term, setTerm] = useState(e.termination_date ?? "");
  const m = useMutation({
    mutationFn: () =>
      api.patch(`/employees/${e.id}`, {
        title,
        org_unit_id: Number(unit),
        ...(term && term !== e.termination_date ? { termination_date: term } : {}),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["employee", String(e.id)] }),
  });
  return (
    <Card title="Kartı düzenle">
      <form className="form-grid" onSubmit={(ev) => { ev.preventDefault(); m.mutate(); }}>
        <label>Unvan<input value={title} onChange={(ev) => setTitle(ev.target.value)} /></label>
        <label>
          Birim (transfer)
          <select value={unit} onChange={(ev) => setUnit(ev.target.value)}>
            {units.data?.items.map((u) => <option key={u.id} value={u.id}>{u.name}</option>)}
          </select>
        </label>
        <label>İşten ayrılış<input type="date" value={term} onChange={(ev) => setTerm(ev.target.value)} /></label>
        <FormError error={m.error} />
        <button className="btn" disabled={m.isPending}>{m.isSuccess ? "Kaydedildi ✓" : "Kaydet"}</button>
      </form>
    </Card>
  );
}
