// Number, money, time and label formatting (owner-approved design: Persian digits, «٬» thousands,
// «٫» decimal, money in toman: م.ت = million, همت = thousand billion; sign + arrow with colour).
import type { ClassName } from "./types";

/** Shown for every missing or unavailable input: never 0 (rule 1). */
export const UNAVAILABLE = "داده در دسترس نیست";

const TEHRAN = "Asia/Tehran";
const nf = new Map<number, Intl.NumberFormat>();

function fmt(digits: number): Intl.NumberFormat {
  let f = nf.get(digits);
  if (!f) {
    f = new Intl.NumberFormat("fa-IR", { minimumFractionDigits: digits, maximumFractionDigits: digits, useGrouping: true });
    nf.set(digits, f);
  }
  return f;
}

/** Persian digits, «٬» grouping, «٫» decimal; no sign (use signed()). */
export function num(x: number, digits = 0): string {
  return fmt(digits).format(Math.abs(x));
}

/** Minus sign used in front of negative numbers. */
export const MINUS = "−";

/** A number with its sign ("+" only when plus is true); the caller isolates it as LTR. */
export function signedNum(x: number, digits = 0, plus = true): string {
  const s = num(x, digits);
  if (roundsToZero(x, digits)) return num(0, digits);
  return (x < 0 ? MINUS : plus ? "+" : "") + s;
}

function roundsToZero(x: number, digits: number): boolean {
  return Math.round(Math.abs(x) * 10 ** digits) === 0;
}

export type Direction = "up" | "down" | "flat";

export function direction(x: number, digits = 2): Direction {
  if (roundsToZero(x, digits)) return "flat";
  return x > 0 ? "up" : "down";
}

export function arrow(d: Direction): string {
  return d === "up" ? "▲" : d === "down" ? "▼" : "";
}

/** Percent with two decimals, e.g. «۲٫۱۰٪». */
export function pct(x: number, digits = 2): string {
  return signedNum(x, digits, true) + "٪";
}

export const RIAL_PER_TOMAN = 10;
const MT = 1e6; // million toman
const HEMMAT = 1e12; // thousand billion toman

export type MoneyUnit = "hemmat" | "mt";

export const UNIT_LABEL: Record<MoneyUnit, string> = { hemmat: "همت", mt: "م.ت" };

/** Unit for an amount in rial: همت from 0.1 همت up, else م.ت. */
export function moneyUnit(rial: number): MoneyUnit {
  return Math.abs(rial) / RIAL_PER_TOMAN >= HEMMAT / 10 ? "hemmat" : "mt";
}

/** Amount in the unit (no label): همت with one decimal, م.ت whole. */
export function moneyIn(rial: number, unit: MoneyUnit, signed = false): string {
  const toman = rial / RIAL_PER_TOMAN;
  const [v, d] = unit === "hemmat" ? [toman / HEMMAT, 1] : [toman / MT, 0];
  return signed ? signedNum(v, d) : (v < 0 && !roundsToZero(v, d) ? MINUS : "") + num(v, d);
}

/** Amount with its unit label, e.g. «۱۰٫۴ همت», «−۴۲ م.ت». */
export function money(rial: number, signed = false, unit: MoneyUnit = moneyUnit(rial)): string {
  return `${moneyIn(rial, unit, signed)} ${UNIT_LABEL[unit]}`;
}

/** Tehran wall-clock time «۱۲:۱۸:۴۲» (seconds optional). */
export function clock(t: Date, seconds = true): string {
  return new Intl.DateTimeFormat("fa-IR", {
    timeZone: TEHRAN, hour: "2-digit", minute: "2-digit", second: seconds ? "2-digit" : undefined, hourCycle: "h23",
  }).format(t);
}

/** Jalali date «شنبه ۵ مهر ۱۴۰۵» (Tehran). */
export function jalaliDate(t: Date): string {
  const parts = new Intl.DateTimeFormat("fa-IR-u-ca-persian", {
    timeZone: TEHRAN, weekday: "long", day: "numeric", month: "long", year: "numeric",
  }).formatToParts(t);
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? "";
  return `${get("weekday")} ${get("day")} ${get("month")} ${get("year")}`;
}

/** Data age «۳ ثانیه پیش» / «۲ دقیقه پیش» / «۱ ساعت پیش»; negative ages (clock skew) are «هم‌اکنون». */
export function age(ms: number): string {
  if (ms < 1000) return "هم‌اکنون";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${num(s)} ثانیه پیش`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${num(m)} دقیقه پیش`;
  return `${num(Math.floor(m / 60))} ساعت پیش`;
}

export const CLASS_LABEL: Record<ClassName, string> = {
  stock: "سهام",
  equity_etf: "صندوق سهامی",
  other_fund: "سایر صندوق‌ها",
  fixed_income: "درآمد ثابت",
  gold: "طلا",
  silver: "نقره",
  unknown: "نامعلوم",
};

/** Colour family of a class: stock, equity ETFs and other funds share the stock colour (owner decision). */
export function classFamily(c: ClassName): "stock" | "fixed" | "gold" | "silver" | "unknown" {
  switch (c) {
    case "stock":
    case "equity_etf":
    case "other_fund":
      return "stock";
    case "fixed_income":
      return "fixed";
    case "gold":
      return "gold";
    case "silver":
      return "silver";
    default:
      return "unknown";
  }
}
