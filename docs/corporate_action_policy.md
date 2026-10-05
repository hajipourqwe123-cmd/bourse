# Corporate Action Policy (Phase 5A)

Implementation: `internal/history/adjust.go`.

## Three price layers (never mixed)

| Layer | Content | Used for |
| --- | --- | --- |
| Raw traded prices | `RawBar`, exactly as printed | execution, locks, limits, rial thresholds, storage (immutable) |
| Adjusted analytical prices | `AdjustedBar`, produced by `Adjust` | returns, ATR, MAs, indicators, labels' return arithmetic |
| Execution prices | `Fill`, from raw bars with an `Assumption` | simulated trades |

`AdjustedBar` is a separate type; the fill functions accept only `RawBar`. Adjustment never mutates raw data (tested).

## Event detection (D4, `py_ratio`)

Event at row t when `py(t) != pc(t-1)`, where t-1 is the previous row with a close. Ratio `r = py(t) / pc(t-1)`. Earlier rows are multiplied by `r` (anchor: latest row, factor 1). Zero-volume rows with unchanged base price create no event. `r > 1` is flagged `Review` (not an expected dilution/dividend; one such case in the Foolad sample, 1.0032 on 1402/01/08).

## Kinds

| Kind | Source of truth | Price effect |
| --- | --- | --- |
| cash dividend | Codal announcement + payment date | `r < 1` on ex-dividend day |
| capital increase (retained earnings / bonus) | Codal / TSETMC | `r < 1` |
| rights issue | Codal | `r < 1`, depends on subscription price |
| splits / par-value changes | Codal | `r` = ratio |
| symbol rename | symbol-history table | none; identity mapping by `ins_code`, not ticker |
| merger | Codal | identity break; series ends, successor starts (survivorship policy) |
| reopening after halt | state model | may gap; **not** a corporate action by itself |

From prices alone every event is `PRICE_DISCONTINUITY`; the kind is attached only when an event source supplies it (Codal ingestion is unassessed; Q-SG4). Adjustment does not depend on the kind, so missing kinds do not block backtests, but cash-dividend total return vs. price return stays unresolved until kinds are known.

## Rules

1. Identity key is `ins_code`; ticker is a label.
2. Rial-valued thresholds (liquidity, order caps, stops) use raw prices on that day.
3. Adjusted prices never feed fills, locks, limit checks or `closing_price`-vs-range tests.
4. Adjustment factors are stored with the event list so any series is reproducible.
