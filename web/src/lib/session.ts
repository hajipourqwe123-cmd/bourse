// Session strip geometry and status (from the gateway's calendar sessions; nothing hard-coded
// but the display axis 08:00–18:00 Tehran).
import { clock, num } from "./format";
import type { ClassName, SessionInfo } from "./types";

export const AXIS_START_H = 8;
export const AXIS_END_H = 18;
/** «N دقیقه تا پایان» is shown (warn colour) from this many minutes before the close. */
export const CLOSING_SOON_MIN = 15;

/** Display rows of the strip (board: three rows; classes with one session share a row). */
export const SESSION_ROWS: { id: "fixed" | "stock" | "metals"; label: string; classes: ClassName[] }[] = [
  { id: "fixed", label: "درآمد ثابت", classes: ["fixed_income"] },
  { id: "stock", label: "سهام و صندوق سهامی", classes: ["stock", "equity_etf", "other_fund"] },
  { id: "metals", label: "طلا و نقره", classes: ["gold", "silver"] },
];

export interface RowSession {
  open: boolean; // at least one class of the row trades today
  preOpen: number; // ms
  start: number;
  close: number;
  verified: boolean;
}

/** Envelope of the row's open classes: earliest pre-open and start, latest close. */
export function rowSession(sessions: SessionInfo[], classes: ClassName[]): RowSession {
  const open = sessions.filter((s) => classes.includes(s.class) && s.open);
  if (open.length === 0) return { open: false, preOpen: 0, start: 0, close: 0, verified: true };
  return {
    open: true,
    preOpen: Math.min(...open.map((s) => Date.parse(s.pre_open))),
    start: Math.min(...open.map((s) => Date.parse(s.start))),
    close: Math.max(...open.map((s) => Date.parse(s.close))),
    verified: open.every((s) => s.verified),
  };
}

/** Tehran hour-of-day of t (ms), as fractional hours. */
export function tehranHours(t: number): number {
  const parts = new Intl.DateTimeFormat("en-GB", { timeZone: "Asia/Tehran", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" })
    .formatToParts(new Date(t));
  const g = (k: string) => Number(parts.find((p) => p.type === k)?.value ?? 0);
  return g("hour") + g("minute") / 60 + g("second") / 3600;
}

/** Position of t on the 08:00–18:00 axis, clamped to [0, 1] (0 = 08:00, left; the axis is LTR). */
export function axisPos(t: number): number {
  const x = (tehranHours(t) - AXIS_START_H) / (AXIS_END_H - AXIS_START_H);
  return Math.min(1, Math.max(0, x));
}

export type SessionPhase = "closed" | "before" | "pre_open" | "trading" | "ended";

export function phase(s: RowSession, now: number): SessionPhase {
  if (!s.open) return "closed";
  if (now < s.preOpen) return "before";
  if (now < s.start) return "pre_open";
  if (now < s.close) return "trading";
  return "ended";
}

/** Status text and whether it is the warn («closing soon») state. */
export function statusText(s: RowSession, now: number): { text: string; warn: boolean } {
  const hm = (t: number) => clock(new Date(t), false);
  switch (phase(s, now)) {
    case "closed":
      return { text: "امروز بسته", warn: false };
    case "before":
      return { text: `پیش‌گشایش از ${hm(s.preOpen)}`, warn: false };
    case "pre_open":
      return { text: `پیش‌گشایش · گشایش ${hm(s.start)}`, warn: false };
    case "trading": {
      const left = Math.ceil((s.close - now) / 60_000);
      if (left <= CLOSING_SOON_MIN) return { text: `${num(left)} دقیقه تا پایان`, warn: true };
      return { text: `در جریان · تا ${hm(s.close)}`, warn: false };
    }
    case "ended":
      return { text: `پایان‌یافته · ${hm(s.close)}`, warn: false };
  }
}
