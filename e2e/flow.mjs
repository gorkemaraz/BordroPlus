// UI flow: the employee requests leave through the form, the manager
// approves it from the queue, the employee's available balance drops.
// HR then cancels the approved day, which restores the balance, so the flow
// leaves no trace and can run again.
import { chromium } from "playwright";

const base = process.env.BASE ?? "http://localhost:3000";
const pw = process.env.PW ?? "Demo.2026!";

// A Monday about seven weeks ahead (one day of leave, never a weekend).
const d = new Date();
d.setDate(d.getDate() + 49);
while (d.getDay() !== 1) d.setDate(d.getDate() + 1);
const day = d.toISOString().slice(0, 10);

const browser = await chromium.launch();
async function as(user) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "tr-TR" });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => { throw e; });
  await page.goto(base + "/");
  await page.fill('input[type="email"]', `${user}@bordroplus.test`);
  await page.fill('input[type="password"]', pw);
  await page.click('button:has-text("Giriş yap")');
  await page.waitForSelector(".sidenav");
  return page;
}
const available = async (page) => {
  await page.goto(base + "/leave?tab=mine");
  const v = page.locator(".stat", { hasText: "Kullanılabilir" }).locator(".stat-value");
  await v.waitFor();
  return parseFloat((await v.textContent()).replace(",", "."));
};

const worker = await as("calisan");
const before = await available(worker);
await worker.fill('label:has-text("Başlangıç") input', day);
await worker.fill('label:has-text("Bitiş") input', day);
await worker.fill('label:has-text("Açıklama") input', "UI akış testi");
await worker.click('button:has-text("Talep gönder")');
await worker.waitForSelector("text=Talep oluşturuldu");
const pendingAfter = await available(worker);

const mgr = await as("yonetici");
await mgr.goto(base + "/leave?tab=approvals");
const row = mgr.locator("tr", { hasText: "Can Demir" }).filter({ hasText: day.split("-").reverse().join(".") });
await row.locator('button:has-text("Onayla")').click();
await row.waitFor({ state: "detached" });

const after = await available(worker);

// HR cancels the approved day: it returns to the balance, and the flow can
// run again.
const hr = await as("ik");
await hr.goto(base + "/leave?tab=team");
await hr.selectOption('select[aria-label="Durum"]', "approved");
const booked = hr.locator("tr", { hasText: "Can Demir" }).filter({ hasText: day.split("-").reverse().join(".") });
await booked.locator('button:has-text("İptal et")').click();
await hr.locator("tr", { hasText: "Can Demir" }).filter({ hasText: day.split("-").reverse().join(".") }).waitFor({ state: "detached" });
const restored = await available(worker);

console.log(`available: before ${before}, after request ${pendingAfter}, after approval ${after}, after HR cancel ${restored}`);
if (!(pendingAfter === before - 1 && after === before - 1 && restored === before)) {
  console.log("unexpected balances");
  process.exit(1);
}
await browser.close();
console.log("flow ok");
