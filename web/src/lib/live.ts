// Connects the store to the gateway: clock offset, state, Centrifugo channels, resync on gaps.
import { Centrifuge } from "centrifuge";
import { estimateOffset, type ClockSample } from "./latency";
import type { MarketStore } from "./store";
import type { SignalMsg, State, SummaryMsg, SymbolsMsg } from "./types";

/** Gateway origin: same origin when served by the gateway; NEXT_PUBLIC_GATEWAY_URL for `next dev`. */
export const GATEWAY = process.env.NEXT_PUBLIC_GATEWAY_URL ?? "";

const CLOCK_EVERY_MS = 5 * 60_000;
const CLOCK_SAMPLES = 5;

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(GATEWAY + path, { cache: "no-store" });
  if (!r.ok) throw new Error(`${path}: HTTP ${r.status}`);
  return (await r.json()) as T;
}

export async function syncClock(store: MarketStore): Promise<void> {
  const samples: ClockSample[] = [];
  for (let i = 0; i < CLOCK_SAMPLES; i++) {
    const t0 = Date.now();
    try {
      const { now } = await getJSON<{ now: number }>("/api/v1/time");
      samples.push({ t0, server: now, t1: Date.now() });
    } catch {
      /* try the next sample */
    }
  }
  const est = estimateOffset(samples);
  if (est) store.setOffset(est.offset);
}

async function loadState(store: MarketStore): Promise<State> {
  for (let wait = 500; ; wait = Math.min(wait * 2, 5000)) {
    try {
      const st = await getJSON<State>("/api/v1/state");
      store.applyState(st);
      return st;
    } catch {
      store.setConnection("offline");
      await new Promise((r) => setTimeout(r, wait));
    }
  }
}

/** Starts the live connection; returns a stop function. */
export function startLive(store: MarketStore): () => void {
  let stopped = false;
  let client: Centrifuge | null = null;
  let resyncing = false;
  const resync = async () => {
    if (resyncing || stopped) return;
    resyncing = true;
    try {
      await loadState(store);
    } finally {
      resyncing = false;
    }
  };
  const clockTimer = setInterval(() => void syncClock(store), CLOCK_EVERY_MS);

  void (async () => {
    await syncClock(store);
    const st = await loadState(store);
    if (stopped) return;
    if (!st.dev_token) {
      // No token issuer (real auth arrives in Sprint 7): the dashboard shows the loaded state only.
      store.setConnection("disabled");
      return;
    }
    client = new Centrifuge(st.centrifugo_ws, {
      getToken: async () => (await getJSON<{ token: string }>("/api/v1/token")).token,
    });
    client.on("connecting", () => store.setConnection("connecting"));
    client.on("connected", () => store.setConnection("live"));
    client.on("disconnected", () => store.setConnection("offline"));

    const symbols = client.newSubscription("mkt:symbols");
    symbols.on("publication", (ctx) => {
      if (store.applySymbols(ctx.data as SymbolsMsg, Date.now()) === "gap") void resync();
    });
    symbols.on("subscribed", (ctx) => {
      // First subscription, or history could not cover the outage: reload the full state.
      if (!ctx.recovered) void resync();
    });
    client.newSubscription("mkt:summary").on("publication", (ctx) => {
      if (store.applySummary(ctx.data as SummaryMsg) === "gap") void resync();
    });
    client.newSubscription("radar:signals").on("publication", (ctx) => store.applySignal(ctx.data as SignalMsg));
    for (const s of client.subscriptions() ? Object.values(client.subscriptions()) : []) s.subscribe();
    client.connect();
  })();

  return () => {
    stopped = true;
    clearInterval(clockTimer);
    client?.disconnect();
  };
}
