// Source→browser latency with browser clock-skew correction (owner change 1).

/** One /api/v1/time exchange: t0/t1 are browser ms around the request, server is the server ms. */
export interface ClockSample {
  t0: number;
  server: number;
  t1: number;
}

/**
 * NTP-style offset (server − browser) from the sample with the smallest round trip: the server
 * time is assumed to be read half-way through it (RTT/2), at browser time `at`. null without a
 * usable sample.
 */
export function estimateOffset(samples: ClockSample[]): { offset: number; rtt: number; at: number } | null {
  let best: ClockSample | null = null;
  for (const s of samples) {
    if (!(s.t1 >= s.t0) || !Number.isFinite(s.server)) continue;
    if (!best || s.t1 - s.t0 < best.t1 - best.t0) best = s;
  }
  if (!best) return null;
  const at = (best.t0 + best.t1) / 2;
  return { offset: best.server - at, rtt: best.t1 - best.t0, at };
}

export type LatencyBasis = "source" | "ingest";

/**
 * Latency of one message: (browser receive time + offset) − the value's source time; when the
 * source time was estimated by the collector (est) the ingest time is used instead and the
 * figure is labelled «از زمان دریافت». rate is the demo clock's speed (DEMO_CLOCK; 1 otherwise):
 * the result is always in real milliseconds.
 */
export function latencyMs(receivedMs: number, offsetMs: number, src: string, ing: string, est: boolean | undefined, rate = 1): { ms: number; basis: LatencyBasis } {
  const basis: LatencyBasis = est ? "ingest" : "source";
  const from = Date.parse(basis === "ingest" ? ing : src);
  return { ms: (receivedMs + offsetMs - from) / rate, basis };
}

export const BASIS_LABEL: Record<LatencyBasis, string> = { source: "از منبع", ingest: "از زمان دریافت" };

/** Nearest-rank percentile of values (p in 0..100); null when empty. */
export function percentile(values: number[], p: number): number | null {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const rank = Math.ceil((p / 100) * sorted.length);
  return sorted[Math.min(sorted.length, Math.max(1, rank)) - 1];
}

/** Sliding window of latency samples (default 5 minutes). */
export class LatencyWindow {
  private samples: { at: number; ms: number; basis: LatencyBasis }[] = [];
  constructor(private readonly spanMs = 5 * 60_000) {}

  add(at: number, ms: number, basis: LatencyBasis) {
    this.samples.push({ at, ms, basis });
    this.trim(at);
  }

  private trim(now: number) {
    let i = 0;
    while (i < this.samples.length && this.samples[i].at < now - this.spanMs) i++;
    if (i > 0) this.samples.splice(0, i);
  }

  /** p95 over the window; the basis is «ingest» if any sample in it was measured from ingest. */
  p95(now: number): { ms: number; basis: LatencyBasis; n: number } | null {
    this.trim(now);
    const v = percentile(this.samples.map((s) => s.ms), 95);
    if (v === null) return null;
    const basis: LatencyBasis = this.samples.some((s) => s.basis === "ingest") ? "ingest" : "source";
    return { ms: v, basis, n: this.samples.length };
  }
}
