// Two tabs of one browser reload at the same moment: both try to restore the
// session with the same refresh cookie. Without cross-tab coordination the
// second refresh replays a used token, the server revokes the family and
// both tabs are logged out (ADR-004). Both must stay logged in.
import { chromium } from "playwright";

const base = process.env.BASE ?? "http://localhost:3000";
const pw = process.env.PW ?? "Demo.2026!";

const browser = await chromium.launch();
const ctx = await browser.newContext();
const a = await ctx.newPage();
await a.goto(base + "/");
await a.fill('input[type="email"]', "calisan@bordroplus.test");
await a.fill('input[type="password"]', pw);
await a.click('button:has-text("Giriş yap")');
await a.waitForSelector(".sidenav");
const b = await ctx.newPage();
await b.goto(base + "/");
await b.waitForSelector(".sidenav");

let failures = 0;
for (let round = 1; round <= 5; round++) {
  await Promise.all([a.reload(), b.reload()]);
  for (const [name, p] of [["A", a], ["B", b]]) {
    const ok = await p.waitForSelector(".sidenav", { timeout: 8000 }).then(() => true, () => false);
    if (!ok) {
      console.log(`round ${round}: tab ${name} lost its session`);
      failures++;
    }
  }
  if (failures) break;
}
await browser.close();
if (failures) process.exit(1);
console.log("tabs ok: 5 simultaneous double reloads, both tabs stayed logged in");
