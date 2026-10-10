# Canonical Quality Gates (Phase 5B)

Code: `internal/canonical/quality.go`. Every gate **detects**; none repairs. A detection becomes a quality code on the row and a counted finding in the report. Repairs live in the normalized layer as separate, traceable rows and never overwrite a raw observation.

## Gates

| Code | Detects | Measured on the 19.5-year sample |
| --- | --- | --- |
| `WRONG_SERVED_DATE` | `served_date != requested_date` | 0 (History is per-instrument, not per-day; the tablokhani matrix showed 102 of 248) |
| `MISSING_TRADING_DAY` | a verified trading day with no row for this instrument | 0 within the instrument's own range |
| `DUPLICATE_OBSERVATION` | more than one row per instrument-day | 0 |
| `IMPOSSIBLE_OHLC` | `high < low`, non-positive high/low on a traded day, or first/last outside `[low, high]` | **1** (1404-05-07: `first_price = 0` while the stock traded 2956-2969) |
| `NEGATIVE_VOLUME_OR_VALUE` | negative volume, value or trade count | 0 |
| `UNKNOWN_INSTRUMENT` | no security-master identity version covers the instrument-day | **4643 of 4644** |
| `TICKER_COLLISION` | one ticker held by several instruments on a date | 0 on the single snapshot date |
| `CORPORATE_ACTION_DISCONTINUITY` | `previous_close != prior row's closing_price` | **32** in 19.5 years |
| `HALT_ROW_MISCLASSIFIED` | zero volume but a non-zero traded range, classified as halted | 0 |
| `SCHEMA_DRIFT` | response schema fingerprint changed for an endpoint | tracked per raw file by the download plan |
| `TIMESTAMP_AMBIGUITY` | trades reported on a date the calendar does not call a trading day | 0 |
| `CLOSING_PRICE_OUTSIDE_TRADED_RANGE` | exchange close outside `[low, high]` on a traded day | **285 of 4230 traded days (6.7%)** |
| `REAL_LEGAL_EXCEEDS_TOTAL` | real + legal buy volume exceeds total volume | **5** |
| `MISSING_REAL_LEGAL` | no `type=1` row for a day that has a `type=0` row | rows before 2008 |
| `MISSING_POINT_IN_TIME_ATTRIBUTES` | `shares_outstanding` / `base_volume` absent | every historical row (History omits both) |

## How to read the two large counts

**`UNKNOWN_INSTRUMENT` 4643 of 4644** is not noise, it is the headline blocker. The security master is built from a single whole-market snapshot, so every identity version starts on 2026-09-23. No version covers an earlier date, so almost every historical instrument-day has no point-in-time identity. A survivorship-aware universe cannot be constructed until dated identity exists (`security_master.md`).

**`CLOSING_PRICE_OUTSIDE_TRADED_RANGE` 285** is expected provider behavior, not corruption: Iran's base-volume rule moves the closing price only partially when volume is below base volume, so it can land outside every print of the day. It is recorded, never corrected, and never used as an executable price.

`CORPORATE_ACTION_DISCONTINUITY` 32 matches the independently recorded figure in `archive_feasibility.md`. From prices alone each is a `PRICE_DISCONTINUITY`; the kind is attached only when an event source supplies it.

## Design rules

1. **Detect, never silently repair.** Gates do not modify prices or volumes — asserted by test (`TestQualityGatesDetectWithoutRepairing` checks a raw value is unchanged after a run).
2. **OHLC gates only on traded days.** A halted row legitimately carries zero prices; applying price gates to it would manufacture thousands of false findings and could lead to "fixing" valid halt rows.
3. **Absence is not zero.** A field a provider does not publish is declared in `absent_fields`, so it is never read as a zero value or counted as provider disagreement.
4. **Findings are capped, counts are not.** The finding list stops at 500 with `findings_truncated`, while `counts` stays complete, so a whole-market run cannot blow up memory yet still reports totals faithfully.
5. **Severity is the caller's call.** The gates do not rank; `UNKNOWN_INSTRUMENT` blocking research while `MISSING_POINT_IN_TIME_ATTRIBUTES` merely constrains it is a judgement recorded here and in the readiness gate, not hard-coded.

## Coverage metric

`CoverageOf(bars, calendar, from, to)` returns observed days over **verified trading days** in the window. The Pattern Discovery gate wants > 98% on verified trading dates, or explicitly quantified missingness shown not to bias the study. Coverage against a weekday-derived calendar would be meaningless, which is why the trading calendar must be evidence-based first.
