// Client market store: initial /api/v1/state, then Centrifugo deltas ordered by sequence.
import { LatencyWindow, latencyMs, type LatencyBasis } from "./latency";
import type { Row, Signal, SignalMsg, State, Summary, SummaryMsg, SymbolsMsg } from "./types";

export type Connection = "connecting" | "live" | "offline" | "disabled";

/** Latency samples taken per delta (bounded work per message at full market). */
export const SAMPLES_PER_DELTA = 100;
const RADAR_KEEP = 50;

export class MarketStore {
  state: State | null = null;
  rows = new Map<string, Row>();
  summary: Summary | null = null;
  radar: Signal[] = [];
  seq = 0;
  connection: Connection = "connecting";
  offsetMs = 0;
  /** Demo clock speed (DEMO_CLOCK; 1 otherwise) and the browser time the offset was measured at. */
  clockRate = 1;
  clockAt = 0;
  clockSynced = false;
  latency = new LatencyWindow();
  lastLatency: { ms: number; basis: LatencyBasis; n: number } | null = null;
  version = 0;
  /** Set when a delta was lost: the owner must reload the state (resync). */
  needResync = false;

  private buffer: SymbolsMsg[] = [];
  private listeners = new Set<() => void>();

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
  getVersion = () => this.version;

  changed() {
    this.version++;
    for (const l of this.listeners) l();
  }

  setConnection(c: Connection) {
    if (this.connection !== c) {
      this.connection = c;
      this.changed();
    }
  }

  setOffset(ms: number, rate = 1, at = 0) {
    this.offsetMs = ms;
    this.clockRate = rate;
    this.clockAt = at;
    this.clockSynced = true;
  }

  /**
   * The gateway's clock (ms) at browser time browserMs: browser time + offset; under a demo
   * clock (rate ≠ 1) extrapolated from the measurement at the demo speed.
   */
  serverNow(browserMs: number): number {
    return browserMs + this.offsetMs + (this.clockRate - 1) * (browserMs - this.clockAt);
  }

  /** Replaces everything with a full state, then applies buffered newer deltas. */
  applyState(st: State) {
    this.state = st;
    this.seq = st.seq;
    this.rows = new Map(st.rows.map((r) => [r.ins, r]));
    this.summary = st.summary;
    this.radar = st.radar.slice(0, RADAR_KEEP);
    this.needResync = false;
    const buffered = this.buffer.filter((m) => m.seq > this.seq && (!m.day || m.day === st.day)).sort((a, b) => a.seq - b.seq);
    this.buffer = [];
    for (const m of buffered) this.applySymbols(m, NaN);
    this.changed();
  }

  /**
   * Applies one mkt:symbols delta: older or repeated sequences are ignored; a gap sets
   * needResync (and buffers until the state is reloaded). receivedMs (browser clock) feeds the
   * latency window; NaN = do not sample (replayed from the buffer).
   */
  applySymbols(m: SymbolsMsg, receivedMs: number): "applied" | "old" | "gap" | "buffered" {
    if (!this.state || this.needResync) {
      this.buffer.push(m);
      return "buffered";
    }
    if (m.day && m.day !== this.state.day) {
      // The gateway moved to another trading day: reload the whole state.
      this.needResync = true;
      this.buffer.push(m);
      this.changed();
      return "gap";
    }
    if (m.seq <= this.seq) return "old";
    if (m.seq > this.seq + 1) {
      this.needResync = true;
      this.buffer.push(m);
      this.changed();
      return "gap";
    }
    for (const r of m.rows) this.rows.set(r.ins, r);
    this.seq = m.seq;
    if (Number.isFinite(receivedMs) && this.clockSynced) {
      const step = Math.max(1, Math.ceil(m.rows.length / SAMPLES_PER_DELTA));
      for (let i = 0; i < m.rows.length; i += step) {
        const r = m.rows[i];
        const l = latencyMs(this.serverNow(receivedMs), 0, r.src, r.ing, r.est, this.clockRate);
        this.latency.add(receivedMs, l.ms, l.basis);
      }
      this.lastLatency = this.latency.p95(receivedMs);
    }
    this.changed();
    return "applied";
  }

  /** Applies a summary; one of another trading day asks for a reload ("gap"). */
  applySummary(m: SummaryMsg): "applied" | "gap" {
    if (this.state && m.summary.day !== this.state.day) {
      this.needResync = true;
      this.changed();
      return "gap";
    }
    this.summary = m.summary;
    this.changed();
    return "applied";
  }

  applySignal(m: SignalMsg) {
    this.radar = [m.signal, ...this.radar].slice(0, RADAR_KEEP);
    this.changed();
  }
}
