import { describe, expect, it } from "vitest";
import { BOM, toCSV } from "./csv";
import { rowStatus } from "./status";
import { MarketStore } from "./store";
import type { Row, SessionInfo, State } from "./types";

const row = (ins: string, over: Partial<Row> = {}): Row => ({
  ins, sym: "s" + ins, class: "stock", last: 1000, chg: 1, value: 5, net_hot: 0, hot_as_of: null, lagging: false, traded: true, awaiting_reset: false, partial: false,
  src: "2026-09-23T06:30:00Z", ing: "2026-09-23T06:30:01Z", issues: 0, ...over,
});
const state = (seq: number, rows: Row[]): State => ({
  seq, now: "", day: "2026-09-23", sessions: [], calendar_unverified: true, hot_threshold: 2e9, plus_threshold: 1e9,
  stale_after_ms: 30000, centrifugo_ws: "", dev_token: true, summary: null as never, rows, radar: [],
});

describe("store sequencing", () => {
  it("buffers before state, drops old, applies next, flags gaps", () => {
    const s = new MarketStore();
    expect(s.applySymbols({ day: "2026-09-23", seq: 5, gw: "", rows: [row("A", { last: 5 })] }, 0)).toBe("buffered");
    expect(s.applySymbols({ day: "2026-09-23", seq: 6, gw: "", rows: [row("B", { last: 6 })] }, 0)).toBe("buffered");
    s.applyState(state(5, [row("A", { last: 1 })])); // seq 5 already in the state: only 6 applies
    expect(s.seq).toBe(6);
    expect(s.rows.get("A")?.last).toBe(1);
    expect(s.rows.get("B")?.last).toBe(6);
    expect(s.applySymbols({ day: "2026-09-23", seq: 6, gw: "", rows: [row("A", { last: 9 })] }, 0)).toBe("old");
    expect(s.applySymbols({ day: "2026-09-23", seq: 7, gw: "", rows: [row("A", { last: 7 })] }, 0)).toBe("applied");
    expect(s.rows.get("A")?.last).toBe(7);
    expect(s.applySymbols({ day: "2026-09-23", seq: 9, gw: "", rows: [row("A", { last: 9 })] }, 0)).toBe("gap");
    expect(s.needResync).toBe(true);
    expect(s.rows.get("A")?.last).toBe(7);
    s.applyState(state(8, [row("A", { last: 8 })]));
    expect(s.seq).toBe(9);
    expect(s.rows.get("A")?.last).toBe(9);
    expect(s.needResync).toBe(false);
  });
  it("another trading day forces a reload", () => {
    const s = new MarketStore();
    s.applyState(state(3, [row("A")]));
    expect(s.applySymbols({ day: "2026-09-24", seq: 4, gw: "", rows: [row("B")] }, 0)).toBe("gap");
    expect(s.needResync).toBe(true);
    expect(s.rows.has("B")).toBe(false);
    const next = state(0, [row("C")]);
    next.day = "2026-09-24";
    s.applyState(next);
    expect([...s.rows.keys()]).toEqual(["C"]); // yesterday's rows are gone
    expect(s.applySummary({ gw: "", summary: { day: "2026-09-25" } as never })).toBe("gap");
  });
  it("samples latency only with a synced clock", () => {
    const s = new MarketStore();
    s.applyState(state(0, []));
    const src = Date.parse("2026-09-23T06:30:00Z");
    s.applySymbols({ day: "2026-09-23", seq: 1, gw: "", rows: [row("A")] }, src + 1000);
    expect(s.lastLatency).toBeNull();
    s.setOffset(-500);
    s.applySymbols({ day: "2026-09-23", seq: 2, gw: "", rows: [row("A")] }, src + 1000);
    expect(s.lastLatency).toEqual({ ms: 500, basis: "source", n: 1 });
  });
});

const sess = (cls: SessionInfo["class"], start: string, close: string): SessionInfo =>
  ({ class: cls, open: true, pre_open: start, start, close, verified: false });

describe("row status", () => {
  const sessions = [sess("stock", "2026-09-23T05:30:00Z", "2026-09-23T09:00:00Z")];
  const now = Date.parse("2026-09-23T06:31:00Z");
  it("awaiting a source reset comes first", () => {
    expect(rowStatus(row("A", { awaiting_reset: true, partial: true, last: null }), sessions, now, 30_000)).toBe("awaiting");
  });
  it("lagging flow totals are stale", () => {
    expect(rowStatus(row("A", { lagging: true }), sessions, Date.parse("2026-09-23T10:00:00Z"), 30_000)).toBe("stale");
  });
  it("stale only while its session trades", () => {
    expect(rowStatus(row("A"), sessions, now, 30_000)).toBe("stale");
    expect(rowStatus(row("A"), sessions, Date.parse("2026-09-23T10:00:00Z"), 30_000)).toBe("live");
    expect(rowStatus(row("A", { est: true, ing: "2026-09-23T06:30:59Z" }), sessions, now, 30_000)).toBe("live");
  });
  it("priority: partial > incomplete > unknown > live", () => {
    const late = Date.parse("2026-09-23T10:00:00Z");
    expect(rowStatus(row("A", { partial: true, last: null }), sessions, late, 30_000)).toBe("partial");
    expect(rowStatus(row("A", { value: null, class: "unknown" }), sessions, late, 30_000)).toBe("incomplete");
    expect(rowStatus(row("A", { class: "unknown" }), sessions, late, 30_000)).toBe("unknown");
  });
});

describe("csv", () => {
  it("BOM, CRLF, quoting, empty cells for missing (never 0)", () => {
    const csv = toCSV([row("A", { sym: 'x,"y"', last: null, net_hot: -42 })], () => "incomplete");
    expect(csv.startsWith(BOM)).toBe(true);
    const lines = csv.slice(1).split("\r\n");
    expect(lines[0].split(",")[0]).toBe("نماد");
    expect(lines[1]).toBe(`"x,""y""",A,سهام,,1.00,5,-42,داده ناقص,2026-09-23T06:30:00Z`);
    expect(lines[2]).toBe("");
  });
});
