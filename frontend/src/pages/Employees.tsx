import { useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api, newKey } from "../api/client";
import { useAuth } from "../auth";
import { date } from "../format";
import { Card, Empty, ErrorBox, FormError, Loading, PageHeader, Table } from "../ui";
import type { Employee, Page, Unit } from "../types";

export function useUnits() {
  return useQuery({ queryKey: ["units"], queryFn: () => api.get<{ items: Unit[] }>("/org/units"), staleTime: 60_000 });
}

export default function EmployeesPage() {
  const { can } = useAuth();
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [unit, setUnit] = useState("");
  const [terminated, setTerminated] = useState(false);
  const [creating, setCreating] = useState(false);
  const units = useUnits();

  const list = useInfiniteQuery({
    queryKey: ["employees", search, unit, terminated],
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams({ limit: "50" });
      if (search) p.set("q", search);
      if (unit) p.set("org_unit_id", unit);
      if (terminated) p.set("include_terminated", "true");
      if (pageParam) p.set("cursor", pageParam);
      return api.get<Page<Employee>>("/employees?" + p);
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = list.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <>
      <PageHeader
        title="Personel"
        sub="Görebildiğiniz çalışanlar: yetkinize göre tüm şirket ya da yönettiğiniz alt ağaç"
        actions={can("employee.write") && <button className="btn primary" onClick={() => setCreating(!creating)}>Yeni çalışan</button>}
      />
      {creating && <NewEmployee units={units.data?.items ?? []} onDone={(id) => nav(`/employees/${id}`)} />}
      <form
        className="filters"
        onSubmit={(e) => {
          e.preventDefault();
          setSearch(q.trim());
        }}
      >
        <input placeholder="Ad, soyad ya da sicil no" value={q} onChange={(e) => setQ(e.target.value)} maxLength={100} />
        <select value={unit} onChange={(e) => setUnit(e.target.value)} aria-label="Birim">
          <option value="">Tüm birimler</option>
          {units.data?.items.map((u) => (
            <option key={u.id} value={u.id}>
              {"  ".repeat(u.depth - 1)}
              {u.name}
            </option>
          ))}
        </select>
        <label className="check">
          <input type="checkbox" checked={terminated} onChange={(e) => setTerminated(e.target.checked)} /> Ayrılanlar dahil
        </label>
        <button className="btn">Ara</button>
      </form>
      <Card>
        {list.isPending ? (
          <Loading />
        ) : list.isError ? (
          <ErrorBox error={list.error} retry={() => list.refetch()} />
        ) : rows.length === 0 ? (
          <Empty>Ölçütlere uyan çalışan yok.</Empty>
        ) : (
          <>
            <Table
              rows={rows}
              rowKey={(e) => e.id}
              onRow={(e) => nav(`/employees/${e.id}`)}
              cols={[
                { label: "Sicil", cell: (e) => e.employee_no },
                { label: "Ad Soyad", cell: (e) => `${e.first_name} ${e.last_name}` },
                { label: "Unvan", cell: (e) => e.title || "—" },
                { label: "Birim", cell: (e) => e.org_unit_name },
                { label: "İşe giriş", cell: (e) => date(e.hire_date) },
                { label: "Durum", cell: (e) => (e.termination_date ? `Ayrıldı ${date(e.termination_date)}` : "Aktif") },
              ]}
            />
            <div className="more">
              <span className="muted">{rows.length} kayıt</span>
              {list.hasNextPage && (
                <button className="btn" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>
                  {list.isFetchingNextPage ? "Yükleniyor…" : "Daha fazla"}
                </button>
              )}
            </div>
          </>
        )}
      </Card>
    </>
  );
}

function NewEmployee({ units, onDone }: { units: Unit[]; onDone: (id: number) => void }) {
  const qc = useQueryClient();
  const [key] = useState(newKey);
  const [f, setF] = useState({ employee_no: "", first_name: "", last_name: "", title: "", org_unit_id: "", hire_date: "", amount: "", valid_from: "" });
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const m = useMutation({
    mutationFn: () =>
      api.post<Employee>(
        "/employees",
        {
          employee_no: f.employee_no.trim().toUpperCase(),
          first_name: f.first_name,
          last_name: f.last_name,
          title: f.title,
          org_unit_id: Number(f.org_unit_id),
          hire_date: f.hire_date,
          ...(f.amount ? { salary: { amount: f.amount.replace(",", "."), valid_from: f.valid_from || f.hire_date } } : {}),
        },
        { idempotencyKey: key },
      ),
    onSuccess: (e) => {
      qc.invalidateQueries({ queryKey: ["employees"] });
      onDone(e.id);
    },
  });
  return (
    <Card title="Yeni çalışan">
      <form
        className="form-grid"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <label>Sicil no<input required value={f.employee_no} onChange={set("employee_no")} placeholder="E-1001" /></label>
        <label>Ad<input required value={f.first_name} onChange={set("first_name")} /></label>
        <label>Soyad<input required value={f.last_name} onChange={set("last_name")} /></label>
        <label>Unvan<input value={f.title} onChange={set("title")} /></label>
        <label>
          Birim
          <select required value={f.org_unit_id} onChange={set("org_unit_id")}>
            <option value="">Seçin</option>
            {units.map((u) => (
              <option key={u.id} value={u.id}>{u.name}</option>
            ))}
          </select>
        </label>
        <label>İşe giriş<input type="date" required value={f.hire_date} onChange={set("hire_date")} /></label>
        <label>Brüt maaş (TL)<input inputMode="decimal" value={f.amount} onChange={set("amount")} placeholder="45000.00" /></label>
        <label>Maaş geçerlilik<input type="date" value={f.valid_from} onChange={set("valid_from")} /></label>
        <FormError error={m.error} />
        <button className="btn primary" disabled={m.isPending}>{m.isPending ? "Kaydediliyor…" : "Kaydet"}</button>
      </form>
    </Card>
  );
}
