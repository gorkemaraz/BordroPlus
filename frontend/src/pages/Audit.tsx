import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { dateTime, roleNames } from "../format";
import { Card, Empty, ErrorBox, Loading, PageHeader, Table } from "../ui";
import type { AuditRecord, Page } from "../types";

function short(v: unknown): string {
  if (v == null) return "";
  const s = JSON.stringify(v);
  return s.length > 120 ? s.slice(0, 120) + "…" : s;
}

export default function AuditPage() {
  const [action, setAction] = useState("");
  const [entity, setEntity] = useState("");
  const q = useInfiniteQuery({
    queryKey: ["audit", action, entity],
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams({ limit: "50" });
      if (action) p.set("action", action);
      if (entity) p.set("entity_type", entity);
      if (pageParam) p.set("cursor", pageParam);
      return api.get<Page<AuditRecord>>("/audit-log?" + p);
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <>
      <PageHeader title="Denetim kaydı" sub="Eklemeli-yalnız: kayıtlar veritabanında değiştirilemez ve silinemez." />
      <div className="filters">
        <input placeholder="İşlem (ör. payroll.close)" value={action} onChange={(e) => setAction(e.target.value.trim())} />
        <select value={entity} onChange={(e) => setEntity(e.target.value)} aria-label="Varlık">
          <option value="">Tüm varlıklar</option>
          {["user", "employee", "org_unit", "delegation", "leave_request", "punch_correction", "payroll_period", "payroll_item"].map((x) => (
            <option key={x} value={x}>{x}</option>
          ))}
        </select>
      </div>
      <Card>
        {q.isPending ? (
          <Loading />
        ) : q.isError ? (
          <ErrorBox error={q.error} retry={() => q.refetch()} />
        ) : rows.length === 0 ? (
          <Empty>Kayıt yok.</Empty>
        ) : (
          <>
            <Table
              rows={rows}
              rowKey={(r) => r.id}
              cols={[
                { label: "Zaman", cell: (r) => dateTime(r.occurred_at) },
                { label: "Kim", cell: (r) => (r.actor_user_id ? `#${r.actor_user_id} ${roleNames[r.actor_role ?? ""] ?? ""}` : "sistem") },
                { label: "İşlem", cell: (r) => <code>{r.action}</code> },
                { label: "Varlık", cell: (r) => `${r.entity_type}${r.entity_id ? " #" + r.entity_id : ""}` },
                { label: "Değişiklik", cell: (r) => <span className="muted small">{short(r.old_value)}{r.old_value != null ? " → " : ""}{short(r.new_value)}</span> },
              ]}
            />
            <div className="more">
              {q.hasNextPage && <button className="btn" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>Daha fazla</button>}
            </div>
          </>
        )}
      </Card>
    </>
  );
}
