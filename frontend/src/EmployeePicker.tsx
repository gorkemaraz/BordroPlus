import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api/client";
import type { Employee, Page } from "./types";

/** Searches employees within the caller's scope and reports the chosen id. */
export function EmployeePicker({ value, onChange, label = "Çalışan" }: { value: number | null; onChange: (id: number | null, e?: Employee) => void; label?: string }) {
  const [q, setQ] = useState("");
  const list = useQuery({
    queryKey: ["picker", q],
    queryFn: () => api.get<Page<Employee>>(`/employees?limit=30${q ? "&q=" + encodeURIComponent(q) : ""}`),
  });
  return (
    <div className="picker">
      <label>
        {label}
        <input placeholder="Ad ya da sicil no ile ara" value={q} onChange={(e) => setQ(e.target.value)} />
      </label>
      <select
        aria-label={label}
        value={value ?? ""}
        onChange={(e) => {
          const id = e.target.value ? Number(e.target.value) : null;
          onChange(id, list.data?.items.find((x) => x.id === id));
        }}
      >
        <option value="">{list.isPending ? "Yükleniyor…" : list.isError ? "Liste alınamadı" : `Seçin (${list.data?.items.length ?? 0} sonuç)`}</option>
        {list.data?.items.map((e) => (
          <option key={e.id} value={e.id}>
            {e.employee_no} · {e.first_name} {e.last_name} · {e.org_unit_name}
          </option>
        ))}
      </select>
    </div>
  );
}
