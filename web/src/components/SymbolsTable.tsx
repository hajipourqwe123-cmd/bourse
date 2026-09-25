"use client";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useMemo, useRef, useState } from "react";
import { age, num, UNAVAILABLE } from "../lib/format";
import { toCSV } from "../lib/csv";
import { rowStatus, STATUS_LABEL, type RowStatus } from "../lib/status";
import type { Row } from "../lib/types";
import { useMarket, useNow } from "./hooks";
import { ClassChip, Money, NA, Pct } from "./ui";

type Tab = "value" | "rel_volume" | "suspicious" | "hot_in";
const TABS: { id: Tab; label: string; available: boolean }[] = [
  { id: "value", label: "بیشترین ارزش", available: true },
  { id: "rel_volume", label: "حجم نسبی", available: false },
  { id: "suspicious", label: "حجم مشکوک", available: false },
  { id: "hot_in", label: "ورود پول درشت", available: true },
];
const STATUS_CHIP: Record<RowStatus, string> = {
  awaiting: "chip chip-warn",
  live: "chip chip-live",
  stale: "chip chip-stale",
  partial: "chip chip-warn",
  incomplete: "chip chip-muted",
  unknown: "chip chip-muted",
};
const ROW_H = 40;

/** Descending by key; missing values last (never treated as 0). */
function byDesc(key: (r: Row) => number | null) {
  return (a: Row, b: Row) => {
    const x = key(a), y = key(b);
    if (x === null && y === null) return a.ins.localeCompare(b.ins);
    if (x === null) return 1;
    if (y === null) return -1;
    return y - x || a.ins.localeCompare(b.ins);
  };
}

export function SymbolsTable({ query }: { query: string }) {
  const s = useMarket();
  const now = s.serverNow(useNow(5000));
  const [tab, setTab] = useState<Tab>("value");
  const sessions = s.state?.sessions ?? [];
  const staleAfter = s.state?.stale_after_ms ?? 30_000;
  const rows = useMemo(() => {
    const q = query.trim();
    const list = [...s.rows.values()].filter((r) => !q || r.sym.includes(q) || r.ins.includes(q));
    return list.sort(tab === "hot_in" ? byDesc((r) => r.net_hot) : byDesc((r) => r.value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.version, query, tab]);
  const parent = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => parent.current, estimateSize: () => ROW_H, overscan: 8 });
  const status = (r: Row) => rowStatus(r, sessions, now, staleAfter);

  const exportCSV = () => {
    const blob = new Blob([toCSV(rows, status)], { type: "text/csv;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `radar-bazar-${s.summary?.day ?? "today"}.csv`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };

  return (
    <section className="card" aria-labelledby="table-title">
      <h2 className="sr-only" id="table-title">جدول نمادها</h2>
      <div className="card-head">
        <div className="tabs" role="tablist" aria-label="مرتب‌سازی جدول">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              className="tab"
              aria-selected={tab === t.id}
              aria-disabled={!t.available || undefined}
              title={t.available ? undefined : `${UNAVAILABLE} (تاریخچه روزانه، DL-03)`}
              onClick={() => t.available && setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
        <span className="end">
          <button type="button" className="btn" onClick={exportCSV}>
            خروجی CSV (سازگار با اکسل)
          </button>
        </span>
      </div>
      <div className="table" role="table" aria-label="نمادها" aria-rowcount={rows.length + 1}>
        <div className="thead" role="rowgroup">
          <div className="tr" role="row">
            {["نماد", "کلاس", "آخرین (ریال)", "تغییر", "ارزش (م.ت)", "پول درشت (م.ت)", "داده"].map((h) => (
              <span role="columnheader" key={h}>
                {h}
              </span>
            ))}
          </div>
        </div>
        <div className="tbody" ref={parent} role="rowgroup" tabIndex={0} aria-label="ردیف‌های نماد، قابل پیمایش" data-testid="table-body">
          <div style={{ height: v.getTotalSize(), position: "relative" }}>
            {v.getVirtualItems().map((vi) => {
              const r = rows[vi.index];
              const st = status(r);
              return (
                <div key={r.ins} className="tr" role="row" aria-rowindex={vi.index + 2} style={{ transform: `translateY(${vi.start}px)` }}>
                  <span role="cell" className="sym">{r.sym || r.ins}</span>
                  <span role="cell"><ClassChip cls={r.class} /></span>
                  <span role="cell">{r.last === null ? <NA /> : <span className="num">{num(r.last)}</span>}</span>
                  <span role="cell">{r.traded === false ? <span className="na">بی‌معامله</span> : <Pct value={r.chg} />}</span>
                  <span role="cell"><Money rial={r.value} unit="mt" bare /></span>
                  <span role="cell"><Money rial={r.net_hot} signed unit="mt" bare /></span>
                  <span role="cell">
                    <span className={STATUS_CHIP[st]} title={`به‌روز: ${age(now - Date.parse(r.est ? r.ing : r.src))}`}>{STATUS_LABEL[st]}</span>
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      </div>
      <div className="card-foot">
        <span>
          <span className="num">{num(rows.length)}</span> نماد
        </span>
        <span>ستون «حجم به میانگین ۳۰ روز» تا رسیدن تاریخچه روزانه (DL-03) نمایش داده نمی‌شود.</span>
      </div>
    </section>
  );
}
