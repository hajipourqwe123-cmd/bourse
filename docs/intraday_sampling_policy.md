# Intraday Sampling Policy (Phase 5A)

Hybrid, configurable, provider-agnostic. No provider is assumed to supply every frequency.

| Tier | Interval | Content | Stored as |
| --- | --- | --- | --- |
| T1 | 1 min | queue sizes, buyer/seller counts, per-capita (real buy/sell), hot-money-like microstructure | snapshot rows with source timestamp |
| T2 | 1-2 min | price-volume trajectory | snapshot rows |
| T3 | 5 min | technical features, industry flows, breadth/regime | derived from T1/T2 or direct snapshots |
| T4 | daily | EOD features, official daily history | `History.php`-style rows |

## Rules

1. Each tier is a config entry `{interval, provider_chain[], fields[], session_windows[]}`. Intervals and providers are changeable without code changes.
2. A series comes from **one** provider only (ADR-0006 field ownership). Differences between two feeds are not valid features. Fallback providers fill a different series label.
3. Each row stores `source_time` (provider) and `received_time` (ours, with timezone); `source_time_estimated` is set when the provider gives none.
4. Collection windows follow the class session calendar (`market_session_calendar.md`: 12:30, 15:00 or 18:00 close by class); outside an instrument's window it is not polled. A sample is labelled with the completeness state of its day; an intraday sample is never relabelled as an end-of-day value.
5. Missing samples are stored as gaps; nothing is interpolated or forward-filled.
6. Rate limits are configuration values (`*_DAILY_LIMIT`, `*_MIN_INTERVAL`); the daily budget is persisted so restarts cannot exceed it.
7. 1-minute collection is feasible only where the provider budget allows it (see provider_benchmark_plan.md); with the free BrsApi/SourceArena quotas it is not sustainable for the whole market.
