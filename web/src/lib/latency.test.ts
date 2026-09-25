import { describe, expect, it } from "vitest";
import { estimateOffset, LatencyWindow, latencyMs, percentile } from "./latency";

describe("clock offset", () => {
  it("uses the minimum-RTT sample and RTT/2", () => {
    // browser is 2000 ms behind the server
    const est = estimateOffset([
      { t0: 1000, server: 3300, t1: 1400 }, // rtt 400: offset 3300 − 1200 = 2100 (asymmetric)
      { t0: 2000, server: 4050, t1: 2100 }, // rtt 100: offset 4050 − 2050 = 2000
      { t0: 3000, server: 5200, t1: 3500 },
    ]);
    expect(est).toEqual({ offset: 2000, rtt: 100 });
  });
  it("ignores unusable samples", () => {
    expect(estimateOffset([])).toBeNull();
    expect(estimateOffset([{ t0: 5, server: NaN, t1: 6 }, { t0: 9, server: 1, t1: 8 }])).toBeNull();
  });
});

describe("latency", () => {
  it("corrects for skew; source vs ingest basis", () => {
    const src = "2026-09-27T08:00:00.000Z";
    const ing = "2026-09-27T08:00:01.000Z";
    const recv = Date.parse(src) + 1500 - 2000; // browser clock 2 s behind
    expect(latencyMs(recv, 2000, src, ing, false)).toEqual({ ms: 1500, basis: "source" });
    expect(latencyMs(recv, 2000, src, ing, true)).toEqual({ ms: 500, basis: "ingest" });
  });
  it("nearest-rank p95", () => {
    const v = Array.from({ length: 100 }, (_, i) => i + 1);
    expect(percentile(v, 95)).toBe(95);
    expect(percentile([7], 95)).toBe(7);
    expect(percentile([], 95)).toBeNull();
    expect(percentile([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 1000], 95)).toBe(19);
  });
  it("window drops samples older than the span; ingest taints the label", () => {
    const w = new LatencyWindow(1000);
    w.add(0, 5000, "source");
    w.add(900, 100, "source");
    expect(w.p95(950)?.ms).toBe(5000);
    expect(w.p95(1500)).toEqual({ ms: 100, basis: "source", n: 1 });
    w.add(1600, 200, "ingest");
    expect(w.p95(1600)?.basis).toBe("ingest");
  });
});
