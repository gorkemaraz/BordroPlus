// Shared building blocks: every data view goes through <Async>, so loading,
// empty and error states look and behave the same on every screen.
import { useState, type ReactNode } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import { ApiError } from "./api/client";
import { statusNames } from "./format";

export function Loading({ label = "Yükleniyor…" }: { label?: string }) {
  return (
    <div className="state" role="status" aria-live="polite">
      <span className="spinner" aria-hidden /> {label}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="state empty">{children}</div>;
}

export function ErrorBox({ error, retry }: { error: unknown; retry?: () => void }) {
  const e = error instanceof ApiError ? error : null;
  const msg = e?.message ?? (error instanceof Error ? error.message : "Beklenmeyen bir hata oluştu");
  return (
    <div className="state error-box" role="alert">
      <strong>{e?.status === 403 ? "Bu içeriği görme yetkiniz yok." : msg}</strong>
      {e?.fields && (
        <ul>
          {Object.entries(e.fields).map(([k, v]) => (
            <li key={k}>
              {k}: {v}
            </li>
          ))}
        </ul>
      )}
      {e && <small className="muted">Kod: {e.code}</small>}
      {retry && (
        <button className="btn small" onClick={retry}>
          Tekrar dene
        </button>
      )}
    </div>
  );
}

/** Renders loading / error / empty / data for a query. */
export function Async<T>({
  q,
  empty,
  isEmpty,
  children,
}: {
  q: UseQueryResult<T>;
  empty?: ReactNode;
  isEmpty?: (d: T) => boolean;
  children: (d: T) => ReactNode;
}) {
  if (q.isPending) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} retry={() => q.refetch()} />;
  if (isEmpty?.(q.data)) return <Empty>{empty ?? "Kayıt yok."}</Empty>;
  return <>{children(q.data)}</>;
}

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="page-header">
      <div>
        <h1>{title}</h1>
        {sub && <p className="muted">{sub}</p>}
      </div>
      {actions && <div className="actions">{actions}</div>}
    </header>
  );
}

export function Badge({ status }: { status: string }) {
  return <span className={`badge badge-${status}`}>{statusNames[status] ?? status}</span>;
}

export function Card({ title, children, actions }: { title?: ReactNode; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <div className="card-head">
          {title && <h2>{title}</h2>}
          {actions}
        </div>
      )}
      {children}
    </section>
  );
}

export function Tabs({ tabs, value, onChange }: { tabs: { id: string; label: string }[]; value: string; onChange: (id: string) => void }) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button key={t.id} role="tab" aria-selected={t.id === value} className={t.id === value ? "active" : ""} onClick={() => onChange(t.id)}>
          {t.label}
        </button>
      ))}
    </div>
  );
}

/** A responsive table: on narrow screens each row becomes a labelled card. */
export function Table<T>({
  rows,
  cols,
  rowKey,
  onRow,
}: {
  rows: T[];
  cols: { label: string; cell: (r: T) => ReactNode; num?: boolean }[];
  rowKey: (r: T) => string | number;
  onRow?: (r: T) => void;
}) {
  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            {cols.map((c) => (
              <th key={c.label} className={c.num ? "num" : ""}>
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={rowKey(r)} onClick={onRow ? () => onRow(r) : undefined} className={onRow ? "clickable" : ""}>
              {cols.map((c) => (
                <td key={c.label} data-label={c.label} className={c.num ? "num" : ""}>
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Shows a mutation's error under a form. */
export function FormError({ error }: { error: unknown }) {
  if (!error) return null;
  return <ErrorBox error={error} />;
}

/** A button that asks for a reason before running a destructive action. */
export function ReasonButton({ label, onSubmit, busy }: { label: string; onSubmit: (note: string) => void; busy?: boolean }) {
  const [open, setOpen] = useState(false);
  const [note, setNote] = useState("");
  if (!open)
    return (
      <button className="btn small danger" onClick={() => setOpen(true)} disabled={busy}>
        {label}
      </button>
    );
  return (
    <form
      className="inline-form"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(note);
        setOpen(false);
        setNote("");
      }}
    >
      <input autoFocus required placeholder="Gerekçe" value={note} onChange={(e) => setNote(e.target.value)} maxLength={500} />
      <button className="btn small danger" disabled={busy}>
        {label}
      </button>
      <button type="button" className="btn small ghost" onClick={() => setOpen(false)}>
        Vazgeç
      </button>
    </form>
  );
}

export function Stat({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {hint && <span className="stat-hint muted">{hint}</span>}
    </div>
  );
}
