// UI smoke test: every role logs in through the form and opens every page it
// can see, at desktop and phone width. Fails on console errors, uncaught
// exceptions, error boxes, failed API calls (5xx) and horizontal scrolling.
import { chromium } from "playwright";

const base = process.env.BASE ?? "http://localhost:3000";
const password = process.env.PW ?? "Demo.2026!";
const shots = process.env.SHOTS ?? ""; // directory for screenshots, empty = none
const roles = ["calisan", "yonetici", "ik", "bordro", "muhasebe", "denetci"];
const viewports = { desktop: { width: 1280, height: 860 }, phone: { width: 390, height: 844 } };

const browser = await chromium.launch();
const problems = [];
let pages = 0;

for (const [vpName, vp] of Object.entries(viewports)) {
  for (const role of roles) {
    const ctx = await browser.newContext({ viewport: vp, locale: "tr-TR", timezoneId: "Europe/Istanbul" });
    const page = await ctx.newPage();
    const where = () => `${vpName}/${role}${new URL(page.url()).pathname}${new URL(page.url()).search}`;
    page.on("console", (m) => m.type() === "error" && problems.push(`${where()} console: ${m.text()}`));
    page.on("pageerror", (e) => problems.push(`${where()} exception: ${e.message}`));
    page.on("response", (r) => r.status() >= 500 && problems.push(`${where()} HTTP ${r.status()} ${r.url()}`));

    await page.goto(base + "/");
    await page.fill('input[type="email"]', `${role}@bordroplus.test`);
    await page.fill('input[type="password"]', password);
    await page.click('button:has-text("Giriş yap")');
    await page.waitForSelector(".sidenav", { state: "attached" });

    if (vpName === "phone") await page.click(".menu-btn");
    const links = await page.$$eval(".sidenav a", (as) => as.map((a) => a.getAttribute("href")));
    if (vpName === "phone") await page.click(".menu-btn");
    for (const href of links) {
      await page.goto(base + href);
      await page.waitForLoadState("networkidle");
      // Visit every tab of the page as well.
      const tabCount = await page.locator(".tabs button").count();
      for (let i = 0; i < Math.max(1, tabCount); i++) {
        if (tabCount) {
          await page.locator(".tabs button").nth(i).click();
          await page.waitForLoadState("networkidle");
        }
        pages++;
        const errs = await page.$$eval(".error-box", (els) => els.map((e) => e.textContent?.trim()));
        for (const e of errs) problems.push(`${where()} error box: ${e}`);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        if (overflow > 1) problems.push(`${where()} horizontal overflow ${overflow}px`);
        if (shots && (role === "ik" || role === "yonetici" || role === "bordro")) {
          const name = `${vpName}-${role}${href.replace(/\//g, "_")}-${i}.png`;
          await page.screenshot({ path: `${shots}/${name}`, fullPage: vpName === "phone" });
        }
      }
    }
    await ctx.close();
  }
}
await browser.close();
console.log(`${pages} page views checked`);
if (problems.length) {
  console.log(problems.join("\n"));
  process.exit(1);
}
console.log("no problems found");
