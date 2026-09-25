"use client";
// Shared building blocks. Every number goes through Num/Money/Pct: missing → «داده در دسترس نیست».
import { age, arrow, CLASS_LABEL, classFamily, direction, moneyIn, moneyUnit, pct, UNAVAILABLE, UNIT_LABEL, type MoneyUnit } from "../lib/format";
import { useMarket, useNow } from "./hooks";
import type { ClassName } from "../lib/types";
import { ZERO_TIME } from "../lib/types";

export function NA() {
  return <span className="na">{UNAVAILABLE}</span>;
}

/** A signed/coloured amount: sign + arrow + colour together (colour never alone). */
export function Money({ rial, signed = false, unit, bare = false }: { rial: number | null | undefined; signed?: boolean; unit?: MoneyUnit; bare?: boolean }) {
  if (rial === null || rial === undefined) return <NA />;
  const u = unit ?? moneyUnit(rial);
  const d = signed ? (u === "hemmat" ? direction(rial / 1e13, 1) : direction(rial / 1e7, 0)) : "flat";
  // Only the number is an LTR island; the unit follows it in RTL reading order («۹٫۶ همت»).
  return (
    <span className={d === "up" ? "up" : d === "down" ? "down" : undefined}>
      {signed && d !== "flat" ? <span aria-hidden="true">{arrow(d)} </span> : null}
      <span className="num">{moneyIn(rial, u, signed)}</span>
      {bare ? null : ` ${UNIT_LABEL[u]}`}
    </span>
  );
}

export function Pct({ value }: { value: number | null | undefined }) {
  if (value === null || value === undefined) return <NA />;
  const d = direction(value);
  return (
    <span className={d === "up" ? "up" : d === "down" ? "down" : undefined}>
      <span className="num">{pct(value)}</span>
      {d !== "flat" ? <span aria-hidden="true"> {arrow(d)}</span> : null}
    </span>
  );
}

export function ClassChip({ cls }: { cls: ClassName }) {
  return <span className={`chip cls-${classFamily(cls)}`}>{CLASS_LABEL[cls]}</span>;
}

/** «به‌روز: N ثانیه پیش» from a source time, corrected by the browser clock offset. */
export function Age({ at, prefix = "به‌روز:" }: { at: string | null | undefined; prefix?: string }) {
  const now = useNow();
  const store = useMarket();
  if (!at || at === ZERO_TIME) return <span className="t-label">{prefix} {UNAVAILABLE}</span>;
  return (
    <span className="t-label">
      {prefix} {age(store.serverNow(now) - Date.parse(at))}
    </span>
  );
}

export function Unavailable({ title, reason }: { title?: string; reason: string }) {
  return (
    <div className="unavailable" role="note">
      <strong>{title ?? UNAVAILABLE}</strong>
      <span>{reason.replace(/([A-Z]+)-(\d+)/g, "$1\u2011$2") /* task IDs never break at the hyphen */}</span>
    </div>
  );
}

export function MethodLink({ anchor }: { anchor: string }) {
  return (
    <a className="t-label method-link" href={`method/#${anchor}`}>
      روش محاسبه
    </a>
  );
}
