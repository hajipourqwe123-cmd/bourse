// Gate 2 (PRD): p95 source→browser < 5 s on the rebased replay; the full-market table (1500
// symbols) without freezing: virtualized (DOM rows < 100), no long task > 200 ms while updating
// and scrolling. Plus an axe-core accessibility pass (contrast, names/labels, keyboard) and
// screenshots at 1440 and 390. Writes gate2-out/report.json.
import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";

const OUT = "../gate2-out";
const MEASURE_S = Number(process.env.GATE_SECONDS ?? 300);
const MIN_ROWS = Number(process.env.GATE_MIN_ROWS ?? 1500);

type Gate = { p95: number | null; basis: string | null; samples: number; rows: number; seq: number; connection: string; domRows: number; hotAgeMaxMs: number | null };

async function gate(page: Page): Promise<Gate> {
  const g = await page.evaluate(() => (window.__gate ? window.__gate() : null));
  return g ?? { p95: null, basis: null, samples: 0, rows: 0, seq: 0, connection: "loading", domRows: 0, hotAgeMaxMs: null };
}

async function axe(page: Page, name: string) {
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  return {
    name,
    violations: r.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.length, sample: v.nodes.slice(0, 3).map((n) => n.target.join(" ")) })),
  };
}

/** Tabs through the page and reports which key controls received focus. */
async function keyboard(page: Page) {
  const seen = new Set<string>();
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press("Tab");
    const label = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      if (!el) return "";
      return (el.getAttribute("aria-label") || el.textContent || el.getAttribute("placeholder") || el.tagName).trim().slice(0, 40);
    });
    seen.add(label);
  }
  const want = ["جستجوی نماد یا صندوق", "بیشترین ارزش", "ورود پول درشت", "خروجی CSV (سازگار با اکسل)", "ردیف‌های نماد، قابل پیمایش", "اعلان‌ها (به‌زودی)"];
  return { reached: want.filter((w) => seen.has(w)), missing: want.filter((w) => !seen.has(w)) };
}

test("gate 2", async ({ browser }) => {
  mkdirSync(OUT, { recursive: true });
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  await page.addInitScript(() => {
    (window as unknown as { __longTasks: number[] }).__longTasks = [];
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) (window as unknown as { __longTasks: number[] }).__longTasks.push(e.duration);
    }).observe({ type: "longtask", buffered: true });
  });
  await page.goto("./");
  await expect.poll(async () => (await gate(page)).connection, { timeout: 120_000 }).toBe("live");
  await expect.poll(async () => (await gate(page)).rows, { timeout: 180_000 }).toBeGreaterThanOrEqual(MIN_ROWS);
  // Warm-up: the replay fast-forwards the first minute; wait until the engine has caught up
  // (every row's flow totals younger than 10 s), then measure steady state.
  const warm = Date.now();
  await expect.poll(async () => (await gate(page)).hotAgeMaxMs ?? Infinity, { timeout: 300_000, intervals: [2000] }).toBeLessThan(10_000);
  const warmupS = Math.round((Date.now() - warm) / 1000);
  // Measurement window: fresh latency samples and long tasks only.
  await page.evaluate(() => {
    (window as unknown as { __longTasks: number[] }).__longTasks = [];
    window.__gateReset!();
  });
  const body = page.getByTestId("table-body");
  const started = Date.now();
  let maxDom = 0;
  let scrolls = 0;
  while (Date.now() - started < MEASURE_S * 1000) {
    // Scroll through the full-market table while deltas arrive.
    await body.evaluate((el, i) => { el.scrollTop = (i % 20) * (el.scrollHeight / 20); }, scrolls++);
    await page.waitForTimeout(1000);
    maxDom = Math.max(maxDom, (await gate(page)).domRows);
  }
  const g = await gate(page);
  const longTasks: number[] = await page.evaluate(() => (window as unknown as { __longTasks: number[] }).__longTasks);
  const maxLong = longTasks.length ? Math.max(...longTasks) : 0;
  await body.evaluate((el) => { el.scrollTop = 0; });
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${OUT}/desktop-1440.png`, fullPage: true });
  const axeDesktop = await axe(page, "desktop-1440");
  const kb = await keyboard(page);

  const mctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  const m = await mctx.newPage();
  await m.goto("./");
  await expect.poll(async () => (await gate(m)).rows, { timeout: 60_000 }).toBeGreaterThanOrEqual(MIN_ROWS);
  await m.waitForTimeout(2000);
  await m.screenshot({ path: `${OUT}/mobile-390.png`, fullPage: true });
  const axeMobile = await axe(m, "mobile-390");
  // Touch targets ≥ 44 px on mobile (interactive, visible elements).
  const small = await m.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>("button, a, input, [role=tab]")]
      .filter((el) => el.offsetParent !== null)
      .map((el) => ({ el, r: el.getBoundingClientRect() }))
      .filter(({ r }) => r.width > 0 && (r.height < 44 || r.width < 44))
      .map(({ el, r }) => `${(el.textContent || el.getAttribute("aria-label") || el.tagName).trim().slice(0, 30)} ${Math.round(r.width)}×${Math.round(r.height)}`),
  );

  const report = {
    at: new Date().toISOString(),
    measured_s: MEASURE_S,
    warmup_s: warmupS,
    latency_p95_ms: g.p95,
    latency_basis: g.basis,
    latency_samples: g.samples,
    rows: g.rows,
    dom_rows_max: maxDom,
    long_tasks: longTasks.length,
    long_task_max_ms: Math.round(maxLong),
    axe: [axeDesktop, axeMobile],
    keyboard: kb,
    touch_targets_below_44px: small,
    pass: {
      latency: g.p95 !== null && g.p95 < 5000,
      virtualized: maxDom < 100,
      no_freeze: maxLong <= 200,
    },
  };
  writeFileSync(`${OUT}/report.json`, JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  expect(report.pass.latency, `p95 ${g.p95} ms`).toBe(true);
  expect(report.pass.virtualized, `DOM rows ${maxDom}`).toBe(true);
  expect(report.pass.no_freeze, `longest task ${maxLong} ms`).toBe(true);
});
