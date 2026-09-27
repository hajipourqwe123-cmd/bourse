"use client";
import { createContext, useContext, useEffect, useState, useSyncExternalStore } from "react";
import type { MarketStore } from "../lib/store";

export const StoreContext = createContext<MarketStore | null>(null);

/** The store; re-renders the caller on every store change. */
export function useMarket(): MarketStore {
  const store = useContext(StoreContext);
  if (!store) throw new Error("no MarketStore");
  useSyncExternalStore(store.subscribe, store.getVersion, store.getVersion);
  return store;
}

/**
 * Wall clock (browser ms), ticking every periodMs. It is 0 until the component has mounted: the
 * page is prerendered at build time, and a clock read during render would put the BUILD time into
 * the static HTML and a different time into the browser's first render (React hydration error
 * #418). Callers render a placeholder while it is 0 (see NOT_MOUNTED).
 */
export function useNow(periodMs = 1000): number {
  const [now, setNow] = useState(0);
  useEffect(() => {
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), periodMs);
    return () => clearInterval(id);
  }, [periodMs]);
  return now;
}

/** Text shown for a clock-dependent value before mount (identical on the server and the client). */
export const NOT_MOUNTED = "…";
