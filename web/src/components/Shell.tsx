"use client";
import { useEffect, useState } from "react";
import { BASIS_LABEL } from "../lib/latency";
import { applyMode, nextMode, readMode, THEME_LABEL, type ThemeMode } from "../lib/theme";
import { clock, jalaliDate, num } from "../lib/format";
import { useMarket, useNow } from "./hooks";
import { IconBell, IconBriefcase, IconBuilding, IconFilter, IconGrid, IconPie, IconTarget, IconTheme, IconTrend, IconUser, Logo } from "./icons";

/** Theme toggle: auto (system) → light → dark. */
export function ThemeToggle() {
  const [mode, setMode] = useState<ThemeMode>("auto");
  useEffect(() => setMode(readMode()), []);
  const label = `پوسته: ${THEME_LABEL[mode]}`;
  return (
    <button
      className="icon-btn"
      type="button"
      aria-label={label}
      title={label}
      onClick={() => {
        const m = nextMode(mode);
        applyMode(m);
        setMode(m);
      }}
    >
      <IconTheme mode={mode} />
    </button>
  );
}

const NAV = [
  { label: "داشبورد بازار", icon: IconGrid, live: true },
  { label: "شکار سهام", icon: IconTrend },
  { label: "صندوق‌ها", icon: IconBriefcase },
  { label: "صنایع", icon: IconBuilding },
  { label: "رادار سیگنال", icon: IconTarget },
  { label: "فیلترنویسی", icon: IconFilter },
  { label: "هشدارها", icon: IconBell },
  { label: "پرتفوی من", icon: IconPie },
];

export function Sidebar() {
  return (
    <aside className="sidebar" aria-label="ناوبری">
      <div className="brand">
        <Logo />
        <div>
          <div className="brand-name">رادار بازار</div>
          <div className="t-label">نام کاری</div>
        </div>
      </div>
      <nav>
        <ul className="nav">
          {NAV.map(({ label, icon: Icon, live }) => (
            <li key={label}>
              {live ? (
                <a className="nav-item" href="./" aria-current="page">
                  <Icon />
                  {label}
                </a>
              ) : (
                <span className="nav-item" aria-disabled="true" title="به‌زودی">
                  <Icon />
                  {label}
                  <span className="chip chip-muted soon">به‌زودی</span>
                </span>
              )}
            </li>
          ))}
        </ul>
      </nav>
      <DataHealth />
    </aside>
  );
}

/** «سلامت داده»: p95 source→browser latency (skew-corrected), symbols, quality issues, calendar. */
export function DataHealth() {
  const s = useMarket();
  useNow(5000);
  const lat = s.lastLatency;
  const ok = lat !== null && lat.ms < 5000;
  return (
    <section className="health" aria-label="سلامت داده">
      <span className="t-label">سلامت داده</span>
      <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 700 }}>
        <span className="dot" style={{ background: lat === null ? "var(--muted)" : ok ? "var(--up)" : "var(--down)" }} aria-hidden="true" />
        {lat === null ? (
          <span>تأخیر: در حال سنجش</span>
        ) : (
          <span data-testid="latency">
            تأخیر <span className="num">{num(lat.ms / 1000, 1)}</span> ثانیه
          </span>
        )}
      </div>
      {lat !== null && (
        <span className="t-label">
          صدک ۹۵ پنج دقیقه اخیر، {BASIS_LABEL[lat.basis]}
          {s.clockSynced ? "" : " (ساعت همگام نشده)"}
        </span>
      )}
      <span className="t-label">
        <span className="num">{num(s.summary?.instruments ?? s.rows.size)}</span> نماد ·{" "}
        <span className="num">{num(s.summary?.issues ?? 0)}</span> رخداد کیفیت
      </span>
      {(s.summary?.awaiting_reset ?? 0) > 0 && (
        <span className="t-label">
          <span className="num">{num(s.summary?.awaiting_reset ?? 0)}</span> نماد در انتظار بازنشانی منبع
        </span>
      )}
      {(s.summary?.carryover ?? 0) > 0 && (
        <span className="t-label">
          <span className="num">{num(s.summary?.carryover ?? 0)}</span> نماد هنوز داده روز قبل را نشان می‌دهند
        </span>
      )}
      {s.state?.calendar_unverified && <span className="chip chip-warn" style={{ alignSelf: "flex-start" }}>تقویم جلسات تأییدنشده</span>}
    </section>
  );
}

const CONNECTION: Record<string, { label: string; cls: string }> = {
  live: { label: "زنده", cls: "chip chip-live" },
  connecting: { label: "در حال اتصال", cls: "chip chip-muted" },
  offline: { label: "قطع", cls: "chip chip-stale" },
  disabled: { label: "بدون اتصال زنده", cls: "chip chip-muted" },
};

export function LiveChip() {
  const s = useMarket();
  const c = CONNECTION[s.connection];
  return (
    <span className={c.cls} role="status" data-testid="connection">
      {c.label}
    </span>
  );
}

export function DemoChip({ long = true }: { long?: boolean }) {
  const s = useMarket();
  if (!s.summary?.syn) return null;
  return <span className="chip chip-demo">{long ? "داده نمایشی – غیرواقعی" : "داده نمایشی"}</span>;
}

export function Header({ query, onQuery }: { query: string; onQuery: (q: string) => void }) {
  const now = useNow();
  const d = new Date(now);
  return (
    <header className="header">
      <div className="titles desktop-only">
        <h1 className="t-title">داشبورد بازار</h1>
        <span className="t-label">{jalaliDate(d)}</span>
      </div>
      <div className="mobile-only" style={{ alignItems: "center", gap: 10 }}>
        <Logo size={28} />
        <span className="t-card">رادار بازار</span>
      </div>
      <input
        className="search desktop-only"
        type="search"
        placeholder="جستجوی نماد یا صندوق…"
        aria-label="جستجوی نماد یا صندوق"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
      />
      <div className="tools">
        <span className="desktop-only">
          <DemoChip />
        </span>
        <LiveChip />
        <span className="clock num desktop-only" aria-label="ساعت تهران">
          {clock(d)}
        </span>
        <ThemeToggle />
        <button className="icon-btn" type="button" aria-label="اعلان‌ها (به‌زودی)" title="به‌زودی">
          <IconBell />
        </button>
      </div>
    </header>
  );
}

const TABS = [
  { label: "بازار", icon: IconGrid, live: true },
  { label: "شکار", icon: IconTrend },
  { label: "صندوق‌ها", icon: IconBriefcase },
  { label: "رادار", icon: IconTarget },
  { label: "من", icon: IconUser },
];

export function MobileTabBar() {
  return (
    <nav className="tabbar" aria-label="ناوبری پایین">
      {TABS.map(({ label, icon: Icon, live }) => (
        <button key={label} type="button" aria-current={live ? "page" : undefined} aria-disabled={live ? undefined : true} title={live ? undefined : "به‌زودی"}>
          <Icon size={22} />
          {label}
        </button>
      ))}
    </nav>
  );
}
