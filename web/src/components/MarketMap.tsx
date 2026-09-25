"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { num, pct } from "../lib/format";
import { mapBucket, squarify } from "../lib/treemap";
import type { Row } from "../lib/types";
import { useMarket } from "./hooks";
import { MethodLink } from "./ui";

const TILES = 40;

/** «نقشه بازار»: tile size = day value, colour = change vs yesterday (grey = missing change). */
export function MarketMap() {
  const s = useMarket();
  const box = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const { tiles, noValue } = useMemo(() => {
    const all = [...s.rows.values()];
    const withValue = all.filter((r) => r.value !== null && r.value > 0).sort((a, b) => (b.value ?? 0) - (a.value ?? 0));
    const noValue = all.filter((r) => r.value === null).length;
    return { tiles: squarify<Row>(withValue.slice(0, TILES).map((r) => ({ weight: r.value ?? 0, item: r })), { x: 0, y: 0, w: size.w, h: size.h }), noValue };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.version, size.w, size.h]);
  return (
    <section className="card" aria-labelledby="map-title">
      <div className="card-head">
        <h2 className="t-card" id="map-title">نقشه بازار</h2>
        <span className="end t-label">اندازه: ارزش معاملات · {num(TILES)} نماد اول</span>
      </div>
      <div className="map" ref={box} role="img" aria-label="نقشه بازار بر پایه ارزش معاملات و درصد تغییر">
        {tiles.map((t) => {
          const b = mapBucket(t.item.chg);
          const big = t.w > 54 && t.h > 34;
          return (
            <div key={t.item.ins} className={`tile ${b ? `map-${b}` : "map-na"}`} style={{ left: t.x, top: t.y, width: t.w, height: t.h }} title={`${t.item.sym}: ${t.item.chg === null ? "تغییر: داده ناقص" : pct(t.item.chg)}`}>
              {big && (
                <>
                  <div>{t.item.sym || t.item.ins}</div>
                  <div className="num" style={{ fontWeight: 400 }}>{t.item.chg === null ? "داده ناقص" : pct(t.item.chg)}</div>
                </>
              )}
            </div>
          );
        })}
      </div>
      <div className="map-legend" aria-hidden="true">
        <span>−۳٪</span>
        {[5, 4, 3, 2, 1].map((i) => (
          <span key={i} className={`sw map-${i}`} />
        ))}
        <span>+۳٪</span>
      </div>
      <div className="card-foot">
        {noValue > 0 && <span>{num(noValue)} نماد بدون ارزش معاملات روی نقشه نیستند.</span>}
        <MethodLink anchor="map" />
      </div>
    </section>
  );
}
