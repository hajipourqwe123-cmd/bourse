# Survivorship Policy (Phase 5A)

## Problem

BrsApi "all symbols" lists only currently listed instruments (Q-SG11). A backtest on it silently covers survivors only.

## Policy

1. The universe at date t is built from a **point-in-time instrument table** keyed by `ins_code` with `first_seen`, `last_seen`, `status`, `rename_of`, `merged_into`, `source`. The backtest samples the universe at t, never today's list.
2. Sources, in priority order: (a) symbols that appear in dated archives (the tablokhani score and matrix archives list every symbol of each day, so delisted names present on archived days are recovered for 2026); (b) TSETMC official instrument and symbol-history data from an Iran-network host (not reachable here; unassessed); (c) Codal for mergers/delistings; (d) BrsApi History for any `ins_code` we can name.
3. Long-suspended and failed names stay in the universe with state `SUSPENDED`; the trading-state model prevents fills.
4. No source reconstructs pre-2026 delisted names today. The residual exposure is reported, never hidden:
   - `survivorship_exposure = symbols traded in the window but absent from any list / symbols traded` (estimated from the archive for 2026; unknown before).
5. Every backtest report states: universe source, window, number of symbols per day, count of symbols with `last_seen` before the end, and the exposure estimate. A result without this block is invalid.
6. Until a delisted-name source exists, pre-2026 results are tagged `survivor_biased`.
