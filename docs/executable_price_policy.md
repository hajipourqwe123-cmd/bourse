# Executable Price Policy (Phase 5A)

Implementation: `internal/history/execprice.go`.

## Price vocabulary

| Name | Definition | May be a fill price? |
| --- | --- | --- |
| `last_price` (`pl`) | last trade print | yes, if inside [`trade_low`, `trade_high`] |
| `closing_price` (`pc`) | exchange-defined close (volume-weighted rule). **May not be a price at which any trade occurred.** In the Foolad sample `pc` is outside [`pmin`, `pmax`] on **285** of 4230 traded days (recounted in Phase 5B; an earlier figure of 286 was off by one) | **never** |
| `trade_high` / `trade_low` (`pmax` / `pmin`) | extremes of actual prints | bounds for fill feasibility, MFE, MAE |
| `open` (`pf`) | first print | yes |
| `adjusted_price` | analytical series, `adj_*` | **never** |
| `executable_price` | output of the fill functions, always labelled with an `Assumption` | by definition |

## Rules

1. Entry, exit, stop execution, breakout fills, MFE and MAE never read `closing_price`.
2. Market order at the open: `OpenFill` -> `pf`, only if it lies inside the traded range (`open_print`).
3. Limit order: limit at/beyond the open fills at the open; limit inside the range fills at the limit (`limit_touched_in_range`, queue position unknown); otherwise unfilled.
4. Stop / breakout: triggered by `trade_low` / `trade_high`; gap through the trigger fills at the open, else at the trigger (`stop_gap_adjusted`). Intraday path unknown, so these are approximate.
5. Mark-to-market: last print if inside the range, else the close clamped into the range, labelled `last_print_clamped`.
6. MFE/MAE from `trade_high`/`trade_low` only (`ExcursionFromEntry`).
7. No fill in `HALTED`, `SUSPENDED`, `UNKNOWN`; no buy in `LIMIT_UP_LOCKED`, no sell in `LIMIT_DOWN_LOCKED` (queue). Sell in an up-lock and buy in a down-lock are allowed at the lock price, labelled `lock_price_queue`.
8. Daily bars cannot give exact fills: **no fill is ever labelled exact**. Reports must show the share of fills per `Assumption` and a sensitivity run (open vs. worst-of-range).
9. Raw (unadjusted) prices only; adjusted series are a different Go type and cannot reach the fill functions.
10. Labels (`final(e+h)`, X1) that were evaluated on `final` must be re-run on `last` as a sensitivity (archive_feasibility finding 2).
11. Percent fields from third-party archives (tablokhani `priceChangeValues`, `priceLevelValues`) are not prices: they never feed fills, MFE/MAE or state classification.
