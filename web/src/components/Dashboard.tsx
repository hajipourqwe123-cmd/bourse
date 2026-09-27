"use client";
import { useEffect, useState } from "react";
import { jalaliDate, clock } from "../lib/format";
import { startLive } from "../lib/live";
import { LatencyWindow } from "../lib/latency";
import { MarketStore } from "../lib/store";
import { FlowCard, KpiRow, QueueBreadthCard, RadarCard, SessionStrip } from "./Cards";
import { StoreContext, useMarket, useNow } from "./hooks";
import { MarketMap } from "./MarketMap";
import { DemoChip, Header, MobileTabBar, Sidebar } from "./Shell";
import { SymbolsTable } from "./SymbolsTable";

declare global {
  interface Window {
    __gate?: () => { p95: number | null; basis: string | null; samples: number; rows: number; seq: number; connection: string; domRows: number; hotAgeMaxMs: number | null };
    __store?: MarketStore;
    __gateReset?: () => void;
  }
}

export default function Dashboard() {
  const [store] = useState(() => new MarketStore());
  useEffect(() => {
    const stop = startLive(store);
    window.__store = store;
    window.__gateReset = () => {
      store.latency = new LatencyWindow();
      store.lastLatency = null;
    };
    window.__gate = () => ({
      p95: store.lastLatency?.ms ?? null,
      basis: store.lastLatency?.basis ?? null,
      samples: store.lastLatency?.n ?? 0,
      rows: store.rows.size,
      seq: store.seq,
      connection: store.connection,
      domRows: document.querySelectorAll('[data-testid="table-body"] [role="row"]').length,
      // Oldest flow totals behind the rows (engine catch-up; Gate 2 warm-up).
      hotAgeMaxMs: (() => {
        const now = store.serverNow(Date.now());
        let max: number | null = null;
        for (const r of store.rows.values()) if (r.hot_as_of) max = Math.max(max ?? 0, now - Date.parse(r.hot_as_of));
        return max;
      })(),
    });
    return stop;
  }, [store]);
  return (
    <StoreContext.Provider value={store}>
      <Body />
    </StoreContext.Provider>
  );
}

function Body() {
  const s = useMarket();
  const [query, setQuery] = useState("");
  return (
    <div className="app">
      <Sidebar />
      <main className="main">
        <Header query={query} onQuery={setQuery} />
        <MobileDate />
        {s.summary?.unknown_notice && (
          <div className="notice" role="alert">
            نگاشت کلاس نمادها هنوز کامل نشده؛ آمار تفکیکی ناقص است
          </div>
        )}
        {!s.state && <div className="t-label" role="status">در حال بارگذاری وضعیت بازار…</div>}
        {s.state && (
          <>
            <SessionStrip />
            <KpiRow />
            <div className="grid-3">
              <span className="only-desktop">
                <FlowCard />
              </span>
              <span className="only-mobile">
                <FlowCard fixedClass="stock" />
              </span>
              <span className="only-desktop">
                <QueueBreadthCard />
              </span>
              <span className="only-desktop">
                <RadarCard />
              </span>
              <span className="only-mobile">
                <RadarCard limit={2} />
              </span>
            </div>
            <div className="grid-table desktop-only">
              <SymbolsTable query={query} />
              <MarketMap />
            </div>
          </>
        )}
      </main>
      <MobileTabBar />
    </div>
  );
}

function MobileDate() {
  const now = useMarket().serverNow(useNow());
  return (
    <div className="mobile-only" style={{ justifyContent: "space-between", alignItems: "center" }}>
      <span className="t-label">
        {jalaliDate(new Date(now)).split(" ").slice(0, 3).join(" ")} · <span className="num">{clock(new Date(now), false)}</span>
      </span>
      <DemoChip long={false} />
    </div>
  );
}
