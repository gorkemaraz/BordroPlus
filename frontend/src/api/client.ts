// API client. The access token lives in memory only; the refresh token is an
// HttpOnly cookie the browser sends to /api/v1/auth. A 401 triggers one
// refresh shared by every pending request ("single flight"), and tabs share
// the fresh token over a BroadcastChannel, so two tabs never present the same
// refresh token (which the server would treat as theft — ADR-004).

export type FieldErrors = Record<string, string>;

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields?: FieldErrors;

  constructor(status: number, code: string, message: string, fields?: FieldErrors) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

export type Session = {
  access_token: string;
  expires_in: number;
  user: Me;
};

export type Me = {
  id: number;
  email: string;
  role: Role;
  employee_id: number | null;
  team_paths: string[];
  permissions: string[];
};

export type Role = "employee" | "manager" | "hr" | "payroll" | "accounting" | "auditor";

let accessToken: string | null = null;
let refreshing: Promise<Session | null> | null = null;
const listeners = new Set<(s: Session | null) => void>();

const channel = typeof BroadcastChannel !== "undefined" ? new BroadcastChannel("bordroplus-auth") : null;
channel?.addEventListener("message", (e: MessageEvent<Session | null>) => {
  accessToken = e.data?.access_token ?? null;
  listeners.forEach((l) => l(e.data));
});

// A non-sensitive hint that a refresh cookie probably exists, so a fresh page
// load only tries to restore a session when there is one (no needless 401).
const hintKey = "bp_session";
function setHint(on: boolean) {
  try {
    if (on) localStorage.setItem(hintKey, "1");
    else localStorage.removeItem(hintKey);
  } catch {
    // storage unavailable (private mode): restoring simply always tries
  }
}
export function hasSessionHint(): boolean {
  try {
    return localStorage.getItem(hintKey) !== null;
  } catch {
    return true;
  }
}

function publish(s: Session | null, broadcast = true) {
  accessToken = s?.access_token ?? null;
  setHint(s !== null);
  listeners.forEach((l) => l(s));
  if (broadcast) channel?.postMessage(s);
}

/** Subscribe to session changes (login, refresh, logout, other tabs). */
export function onSession(l: (s: Session | null) => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

async function parse(res: Response): Promise<unknown> {
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function toError(status: number, body: unknown): ApiError {
  const e = (body as { error?: { code?: string; message?: string; details?: { fields?: FieldErrors } } })?.error;
  if (e) return new ApiError(status, e.code ?? "error", e.message ?? "Beklenmeyen hata", e.details?.fields);
  if (status === 0) return new ApiError(0, "network", "Sunucuya ulaşılamadı");
  return new ApiError(status, "http_" + status, `Sunucu hatası (${status})`);
}

/** Refresh the session once, however many callers ask at the same time. */
export function refreshSession(): Promise<Session | null> {
  refreshing ??= (async () => {
    try {
      const res = await fetch("/api/v1/auth/refresh", { method: "POST", credentials: "same-origin" });
      if (!res.ok) {
        publish(null);
        return null;
      }
      const s = (await res.json()) as Session;
      publish(s);
      return s;
    } catch {
      return null;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

export async function login(email: string, password: string): Promise<Session> {
  const s = (await request<Session>("POST", "/auth/login", { email, password }, false)) as Session;
  publish(s);
  return s;
}

export async function logout(): Promise<void> {
  try {
    await fetch("/api/v1/auth/logout", { method: "POST", credentials: "same-origin" });
  } finally {
    publish(null);
  }
}

type Options = { idempotencyKey?: string };

async function request<T>(method: string, path: string, body?: unknown, retry = true, opts: Options = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (accessToken) headers["Authorization"] = "Bearer " + accessToken;
  if (opts.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;

  let res: Response;
  try {
    res = await fetch("/api/v1" + path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw toError(0, null);
  }
  if (res.status === 401 && retry && !path.startsWith("/auth/")) {
    const s = await refreshSession();
    if (s) return request<T>(method, path, body, false, opts);
  }
  const data = await parse(res);
  if (!res.ok) throw toError(res.status, data);
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown, opts?: Options) => request<T>("POST", path, body ?? {}, true, opts),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
};

/** Fetch a non-JSON resource (CSV, printable HTML) with the session and open it. */
export async function openAuthed(path: string, filename?: string): Promise<void> {
  const go = () =>
    fetch("/api/v1" + path, { headers: accessToken ? { Authorization: "Bearer " + accessToken } : {} });
  let res = await go();
  if (res.status === 401 && (await refreshSession())) res = await go();
  if (!res.ok) throw toError(res.status, await parse(res));
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  if (filename) {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
  } else {
    window.open(url, "_blank", "noopener");
  }
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/** A fresh Idempotency-Key for one user action. */
export function newKey(): string {
  return crypto.randomUUID();
}
