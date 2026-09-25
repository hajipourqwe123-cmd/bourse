// Data-status chip of a row (Tokens board: زنده, بیات, روز ناقص, داده ناقص, کلاس نامعلوم).
import { rowSession } from "./session";
import type { ClassName, Row, SessionInfo } from "./types";

export type RowStatus = "awaiting" | "stale" | "partial" | "incomplete" | "unknown" | "live";

export const STATUS_LABEL: Record<RowStatus, string> = {
  awaiting: "در انتظار بازنشانی منبع",
  live: "زنده",
  stale: "بیات",
  partial: "روز ناقص",
  incomplete: "داده ناقص",
  unknown: "کلاس نامعلوم",
};

/** Whether the class trades at now (unknown: any class trades). */
export function trading(sessions: SessionInfo[], cls: ClassName, now: number): boolean {
  const classes = cls === "unknown" ? sessions.map((s) => s.class) : [cls];
  return classes.some((c) => {
    const s = rowSession(sessions, [c]);
    return s.open && now >= s.start && now < s.close;
  });
}

/**
 * Most severe status first: awaiting a source reset (the figures are the previous day's) > stale (no new data for staleAfterMs while its session trades, or its
 * flow totals lag behind its latest snapshot) > partial day > missing fields > unknown class > live.
 */
export function rowStatus(r: Row, sessions: SessionInfo[], now: number, staleAfterMs: number): RowStatus {
  if (r.awaiting_reset) return "awaiting";
  const ref = Date.parse(r.est ? r.ing : r.src);
  if (r.lagging || (trading(sessions, r.class, now) && now - ref > staleAfterMs)) return "stale";
  if (r.partial) return "partial";
  if ((r.missing?.length ?? 0) > 0 || r.last === null || r.value === null) return "incomplete";
  if (r.class === "unknown") return "unknown";
  return "live";
}
