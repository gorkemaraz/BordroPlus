import { useState } from "react";
import { useAuth } from "../auth";
import { ErrorBox } from "../ui";

const demo = ["calisan", "yonetici", "ik", "bordro", "muhasebe", "denetci"];

export default function LoginPage() {
  const { login } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  return (
    <main className="login">
      <form
        className="card login-card"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
            await login(email.trim(), password);
          } catch (err) {
            setError(err);
          } finally {
            setBusy(false);
          }
        }}
      >
        <h1>BordroPlus</h1>
        <p className="muted">İnsan Kaynakları, İzin ve Bordro Sistemi</p>
        <label>
          E-posta
          <input type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
        </label>
        <label>
          Parola
          <input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        {error != null && <ErrorBox error={error} />}
        <button className="btn primary" disabled={busy}>
          {busy ? "Giriş yapılıyor…" : "Giriş yap"}
        </button>
        <details className="demo">
          <summary>Demo hesapları</summary>
          <p className="muted">Parola: .env içindeki DEMO_PASSWORD (varsayılan Demo.2026!)</p>
          <div className="demo-list">
            {demo.map((d) => (
              <button type="button" key={d} className="btn small ghost" onClick={() => setEmail(`${d}@bordroplus.test`)}>
                {d}@bordroplus.test
              </button>
            ))}
          </div>
        </details>
      </form>
    </main>
  );
}
