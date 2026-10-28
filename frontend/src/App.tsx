import { useQuery } from "@tanstack/react-query";

type Health = {
  status: string;
  version: string;
  time: string;
  checks: Record<string, string>;
};

async function fetchHealth(): Promise<Health> {
  const res = await fetch("/health");
  const body = await res.json();
  if (!res.ok && !body?.checks) throw new Error(`HTTP ${res.status}`);
  return body as Health;
}

export default function App() {
  const health = useQuery({ queryKey: ["health"], queryFn: fetchHealth, refetchInterval: 10_000 });

  return (
    <main className="container">
      <h1>BordroPlus</h1>
      <p className="muted">İnsan Kaynakları, İzin ve Bordro Sistemi</p>

      <section className="card" aria-live="polite">
        <h2>Sistem durumu</h2>
        {health.isPending && <p>Yükleniyor…</p>}
        {health.isError && <p className="error">API'ye ulaşılamadı: {health.error.message}</p>}
        {health.data && (
          <dl className="kv">
            <dt>Durum</dt>
            <dd className={health.data.status === "ok" ? "ok" : "error"}>{health.data.status}</dd>
            {Object.entries(health.data.checks).map(([k, v]) => (
              <div key={k} className="kv-row">
                <dt>{k}</dt>
                <dd className={v === "ok" ? "ok" : "error"}>{v}</dd>
              </div>
            ))}
            <dt>Sürüm</dt>
            <dd>{health.data.version}</dd>
          </dl>
        )}
      </section>
    </main>
  );
}
