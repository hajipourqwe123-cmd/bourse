import { describe, expect, it } from "vitest";
import { axisPos, phase, rowSession, SESSION_ROWS, statusText } from "./session";
import type { SessionInfo } from "./types";

const day = "2026-09-23";
const t = (hm: string) => `${day}T${hm}:00+03:30`;
const ms = (hm: string) => Date.parse(t(hm));
const S = (cls: SessionInfo["class"], pre: string, start: string, close: string, open = true): SessionInfo =>
  ({ class: cls, open, pre_open: t(pre), start: t(start), close: t(close), verified: false });
const sessions: SessionInfo[] = [
  S("stock", "08:45", "09:00", "12:30"), S("equity_etf", "08:45", "09:00", "12:30"), S("other_fund", "08:45", "09:00", "12:30"),
  S("fixed_income", "08:25", "08:30", "15:00"), S("gold", "11:45", "12:00", "18:00"), S("silver", "11:45", "12:00", "18:00", false),
];

describe("session strip", () => {
  it("three board rows from the calendar", () => {
    expect(SESSION_ROWS.map((r) => r.label)).toEqual(["درآمد ثابت", "سهام و صندوق سهامی", "طلا و نقره"]);
    const metals = rowSession(sessions, ["gold", "silver"]);
    expect(metals.open).toBe(true);
    expect(metals.start).toBe(ms("12:00"));
    expect(metals.verified).toBe(false);
    expect(rowSession(sessions, ["silver"]).open).toBe(false);
  });
  it("LTR axis 08:00–18:00", () => {
    expect(axisPos(ms("08:00"))).toBe(0);
    expect(axisPos(ms("13:00"))).toBe(0.5);
    expect(axisPos(ms("18:00"))).toBe(1);
    expect(axisPos(ms("07:00"))).toBe(0);
    expect(axisPos(ms("19:00"))).toBe(1);
  });
  it("phases and status text", () => {
    const st = rowSession(sessions, ["stock", "equity_etf", "other_fund"]);
    expect(phase(st, ms("08:00"))).toBe("before");
    expect(phase(st, ms("08:50"))).toBe("pre_open");
    expect(phase(st, ms("10:00"))).toBe("trading");
    expect(phase(st, ms("12:30"))).toBe("ended");
    expect(statusText(st, ms("10:00"))).toEqual({ text: "در جریان · تا ۱۲:۳۰", warn: false });
    expect(statusText(st, ms("12:18"))).toEqual({ text: "۱۲ دقیقه تا پایان", warn: true });
    expect(statusText(st, ms("12:15"))).toEqual({ text: "۱۵ دقیقه تا پایان", warn: true });
    expect(statusText(st, ms("12:14")).warn).toBe(false);
    expect(statusText(rowSession(sessions, ["silver"]), ms("12:00")).text).toBe("امروز بسته");
  });
});
