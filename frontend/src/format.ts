// Display helpers. Money arrives as decimal strings ("45000.50") and is
// formatted from the string itself, never through floating point.

export function money(v: string | null | undefined): string {
  if (v == null || v === "") return "—";
  const neg = v.startsWith("-");
  const [whole, frac = "00"] = (neg ? v.slice(1) : v).split(".");
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  return `${neg ? "−" : ""}${grouped},${frac.padEnd(2, "0").slice(0, 2)} ₺`;
}

export function days(n: number | null | undefined): string {
  if (n == null) return "—";
  return n.toLocaleString("tr-TR", { maximumFractionDigits: 1 }) + " gün";
}

export function hours(minutes: number | null | undefined): string {
  if (minutes == null) return "—";
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m ? `${h} sa ${m} dk` : `${h} sa`;
}

const dateFmt = new Intl.DateTimeFormat("tr-TR", { day: "2-digit", month: "2-digit", year: "numeric", timeZone: "UTC" });
const dateTimeFmt = new Intl.DateTimeFormat("tr-TR", {
  day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit", timeZone: "Europe/Istanbul",
});
const timeFmt = new Intl.DateTimeFormat("tr-TR", { hour: "2-digit", minute: "2-digit", timeZone: "Europe/Istanbul" });
const monthFmt = new Intl.DateTimeFormat("tr-TR", { month: "long", year: "numeric", timeZone: "UTC" });

/** "2026-03-15" → "15.03.2026" (civil date, no time zone shift). */
export function date(v: string | null | undefined): string {
  if (!v) return "—";
  return dateFmt.format(new Date(v.slice(0, 10) + "T00:00:00Z"));
}

export function dateTime(v: string | null | undefined): string {
  return v ? dateTimeFmt.format(new Date(v)) : "—";
}

export function time(v: string | null | undefined): string {
  return v ? timeFmt.format(new Date(v)) : "—";
}

/** "2026-03" → "Mart 2026". */
export function month(v: string): string {
  return monthFmt.format(new Date(v + "-01T00:00:00Z"));
}

export function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

/** Today in Istanbul as YYYY-MM-DD. */
export function todayIso(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Europe/Istanbul" }).format(new Date());
}

export function currentMonth(): string {
  return todayIso().slice(0, 7);
}

export function addMonths(ym: string, n: number): string {
  const [y, m] = ym.split("-").map(Number);
  const d = new Date(Date.UTC(y, m - 1 + n, 1));
  return d.toISOString().slice(0, 7);
}

export const roleNames: Record<string, string> = {
  employee: "Çalışan",
  manager: "Yönetici",
  hr: "İK Uzmanı",
  payroll: "Bordro Uzmanı",
  accounting: "Muhasebe",
  auditor: "Denetçi",
};

export const leaveTypes: Record<string, string> = {
  annual: "Yıllık izin",
  sick: "Rapor",
  unpaid: "Ücretsiz izin",
  excuse: "Mazeret izni",
};

export const statusNames: Record<string, string> = {
  pending: "Bekliyor",
  approved: "Onaylandı",
  rejected: "Reddedildi",
  cancelled: "İptal",
  proposed: "Öneri",
  obsolete: "Geçersiz",
  draft: "Taslak",
  review: "Kontrolde",
  closed: "Kapalı",
  open: "Açık",
  not_run: "Hesaplanmadı",
};
