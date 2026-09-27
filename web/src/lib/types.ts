// Mirrors of the gateway's JSON (internal/market, cmd/gateway). Money is rial; null = missing.

export type ClassName = "stock" | "equity_etf" | "other_fund" | "fixed_income" | "gold" | "silver" | "unknown";

export interface Row {
  ins: string;
  sym: string;
  class: ClassName;
  last: number | null;
  chg: number | null;
  value: number | null;
  net_hot: number | null;
  hot_as_of: string | null;
  lagging: boolean;
  traded: boolean; // false: no trade today (chg is null)
  awaiting_reset: boolean; // source still shows the previous day's totals: no figures, not aggregated
  partial: boolean;
  missing?: string[];
  src: string;
  ing: string;
  est?: boolean;
  syn?: boolean;
  issues: number;
}

export interface Pattern {
  rule: "hot_out_retail_in" | "hot_in_retail_out" | "both_in" | "both_out" | "none";
  text: string;
}

export interface ClassFlow {
  class: ClassName;
  instruments: number;
  missing: number;
  lagging: number;
  value_missing: number;
  awaiting: number;
  net_hot: number | null;
  net_hot_plus: number | null;
  net_retail: number | null;
  net_unattributed: number | null;
  value: number | null;
  partial: boolean;
  as_of: string;
  est?: boolean;
  pattern: Pattern | null;
}

export interface Bar {
  at: string;
  value: number | null; // null: window not observed
  partial: boolean;
}

export interface KPI {
  id: "index_total" | "value_stock" | "value_fixed" | "value_metals";
  available: boolean;
  reason?: string;
  classes?: ClassName[];
  value: number | null;
  instruments: number;
  missing: number;
  awaiting: number;
  as_of: string;
  est?: boolean;
  series: Bar[];
  secondary?: { class: ClassName; value: number | null; instruments: number; missing: number; awaiting: number };
  note?: string;
}

export interface Breadth {
  class: ClassName;
  instruments: number;
  missing: number;
  untraded: number;
  awaiting: number;
  floor: number;
  down: number;
  flat: number;
  up: number;
  ceil: number;
  as_of: string;
  est?: boolean;
}

export interface Unavailable {
  available: boolean;
  reason: string;
}

export interface Summary {
  day: string;
  as_of: string;
  syn: boolean;
  est: boolean;
  instruments: number;
  unknown: number;
  unknown_share: number;
  unknown_notice: boolean;
  carryover: number;
  awaiting_reset: number;
  carryover_check: { day: string; expected: string; ok: boolean };
  issues: number;
  flows: ClassFlow[];
  kpis: KPI[];
  breadth: Breadth;
  queues: Unavailable;
}

export interface Signal {
  ins: string;
  sym: string;
  class: ClassName;
  kind: "anomaly" | "absorption" | "support";
  z?: number;
  reason: string;
  at: string;
  syn?: boolean;
}

export interface SessionInfo {
  class: ClassName;
  open: boolean;
  pre_open: string;
  start: string;
  close: string;
  verified: boolean;
}

export interface State {
  seq: number;
  now: string;
  day: string;
  sessions: SessionInfo[];
  calendar_unverified: boolean;
  hot_threshold: number;
  plus_threshold: number;
  stale_after_ms: number;
  centrifugo_ws: string;
  dev_token: boolean;
  summary: Summary;
  rows: Row[];
  radar: Signal[];
  /** DEMO_CLOCK only: now, day and sessions come from a virtual clock (synthetic replay). */
  demo_clock?: { start: string; rate: number };
}

export interface SymbolsMsg {
  seq: number;
  day: string;
  gw: string;
  rows: Row[];
}

export interface SummaryMsg {
  gw: string;
  summary: Summary;
}

export interface SignalMsg {
  gw: string;
  signal: Signal;
}

/** Zero time as serialised by Go (a value without any input). */
export const ZERO_TIME = "0001-01-01T00:00:00Z";
