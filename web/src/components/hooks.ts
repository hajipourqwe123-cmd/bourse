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

/** Wall clock (browser ms), ticking every periodMs. */
export function useNow(periodMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), periodMs);
    return () => clearInterval(id);
  }, [periodMs]);
  return now;
}
