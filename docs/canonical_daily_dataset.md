# Canonical Daily Dataset (Phase 5B)

Code: `internal/canonical/daily.go`. Storage conventions: ClickHouse `market.*` (`infra/clickhouse/001_schema.sql`), research exports as Parquet under `D:\Bourse\data\research\` (outside Git).

## Layers

Raw provider responses are immutable and live outside Git with a sha256 and a manifest entry, exactly as in MI-01b. Normalization reads them and writes canonical rows; it never edits a raw file. Repairs belong to the normalized layer, are separate rows with their own provenance, and never overwrite a raw observation.

```
raw provider response (immutable, checksummed)
  -> canonical DailyBar (normalized, provenance + quality codes)
     -> adjusted analysis series (history.AdjustedBar; separate type)
     -> executable prices (history fill functions; raw bars only)
```

## Fields

`trade_date, instrument_id, first_price, last_price, closing_price, high, low, previous_close, volume, value, trade_count, real_buy_volume, real_sell_volume, legal_buy_volume, legal_sell_volume, real_buy_count, real_sell_count, legal_buy_count, legal_sell_count, shares_outstanding, base_volume, trading_state, state_evidence, source, source_timestamp, requested_date, served_date, quality_codes, raw_sha256, absent_fields`

Three field groups carry semantics that are easy to get wrong:

- **`requested_date` vs `served_date`** stay separate. A provider may answer a request for one day with another day's payload — observed in MI-01b in 102 of 248 matrix responses. A row is never indexed by the requested date alone; a mismatch raises `WRONG_SERVED_DATE`.
- **`absent_fields`** names canonical fields a provider does not publish at all. "Not published" is a contract fact, distinct from "value is zero", and it must never be scored as provider disagreement.
- **`shares_outstanding` / `base_volume`** are point-in-time attributes that BrsApi History does **not** carry; they exist only in the current whole-market snapshot. Historical rows therefore keep 0 and carry `MISSING_POINT_IN_TIME_ATTRIBUTES`. They are never backfilled from today, because doing so would corrupt market cap, free float and every base-volume rule retroactively.

## Price semantics

| Name | Definition | Executable? |
| --- | --- | --- |
| `first_price` / `last_price` / `high` / `low` | raw traded prints | yes, within the traded range |
| `closing_price` | exchange-defined (volume-weighted base-volume rule); **may never have traded** | **never** |
| `adjusted_analysis_price` | separate type, produced by the adjustment layer | **never** |
| `executable_price` | output of the fill functions, always labelled with an `Assumption` | by definition |

Measured on the 19.5-year sample: `closing_price` lies outside `[low, high]` on **285 of 4230 traded days** (6.7%). Entry, exit, stop execution, breakout fills, MFE and MAE never read it (`executable_price_policy.md`).

> Correction to an earlier figure: `archive_feasibility.md` and `data_source_contracts.md` state 286 such days. Recomputed twice independently (Go and Python) over the same recording, the count is **285**. There is one *additional* row with an impossible first price (1404-05-07: `pf = 0` while the stock traded 2956-2969), which is a different defect and is reported as `IMPOSSIBLE_OHLC`.

## Normalization notes

- **Jalali dates.** BrsApi returns `1405-07-01`; the canonical key is ISO Gregorian. `internal/canonical/jalali.go` converts exactly, anchored on independent dates (`1385/12/20 = 2007-03-11`, Nowruz dates) and checked for gapless monotonicity across 31 Jalali years. A one-day error would silently misalign every instrument.
- **Mixed numeric types.** BrsApi sends ints and floats for the same field; SourceArena sends the *same field* quoted on some rows and bare on others within one response. Both are accepted; a value that is genuinely unparsable is a reported problem, and the row is skipped rather than the whole response discarded.
- **Halted rows exist.** A halted day has a row with `volume = 0`, `high = low = 0`, `close = previous_close`. OHLC gates are therefore applied only to traded days, and zero volume is never treated as a price-limit lock.
- **Trading state** comes from the Phase 5A model (`internal/history/state.go`) with its evidence label, not from a provider field.

## Measured output (one instrument, full depth)

| Item | Value |
| --- | --- |
| Canonical rows | 4644 |
| Range | 2007-03-11 .. 2026-09-23 (**19.54 years**) |
| Traded days / zero-volume days | 4230 / 414 |
| States | TRADING 3938, HALTED 238, SUSPENDED 176, LIMIT_UP_LOCKED 145, LIMIT_DOWN_LOCKED 75, REOPENED 72 |
| Real/legal breakdown | present from 2008 (`type=1`), absent earlier → `MISSING_REAL_LEGAL` |

## Rules

1. Raw values are preserved; no gate or normalizer mutates a price or volume.
2. Adjusted values are a different Go type and are never written back into a canonical row.
3. Every row carries `source`, `source_timestamp` and its quality codes; a row without provenance is invalid.
4. `trading_state` is recorded with `state_evidence`; `UNKNOWN` is a legitimate value and is never replaced by a guess.
