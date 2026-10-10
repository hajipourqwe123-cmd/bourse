# Security Master (Phase 5B)

Code: `internal/canonical/security.go`. Proof run: `cmd/canonical` (offline, recorded responses only).

## Why ticker is not identity

The exchange rewrites a ticker on a rename and reissues it to a different company after a delisting. A price series keyed by ticker therefore silently splices two companies together, or loses a company at its rename. Identity must be a code that survives both.

| Candidate | Stability | Role here |
| --- | --- | --- |
| TSETMC instrument code (BrsApi `id`, 17-18 digits) | survives renames; per-instrument, not per-company | **canonical identity** (`instrument_id = "tsetmc:<ins_code>"`) |
| ISIN (`IRO1FOLD0001`) | stable; encodes type, board, company and serial | secondary key; the source of `company_id`, `instrument_class`, `board` |
| Ticker (`l18`) | unstable; reused and rewritten | label only; resolving it **requires a date** |

## Model

`instrument_id, provider_ids, ins_code, isin, ticker, name, company_id, instrument_class, market, board, industry_id, valid_from, valid_to, listing_date, delisting_date, previous_ticker, status, identity_source, confidence, ambiguities`

Attributes are versioned (SCD type 2): a change appends a version and closes the previous one's `valid_to`. `At(id, date)` returns the version that applied on a date, so a backtest reads the identity of the day it simulates, never today's.

`status`: `listed`, `delisted`, `renamed`, `merged`, `suspended_long_term`, `inactive`, `unknown`. `Universe(date, statuses...)` samples point-in-time and includes names that were listed then but are gone now.

## ISIN decomposition

`IR` + instrument-type letter + board/market character + 4-char company code + 4-char serial. The serial is alphanumeric: equities use digits (`IRO1FOLD0001`), debt instruments do not (`IRB3TR3307B1`).

| Letter | Class | Observed |
| --- | --- | --- |
| `O` | `ordinary_share` | 1146 |
| `T` | `fund_unit` | 433 |
| `B` | `debt_instrument` | 597 |
| `E` | `energy_certificate` | 18 |
| `R` | `rights_issue` | 11 |

The **company code** (chars 4-8) is what links instruments without using tickers: a rights issue (`IRR3ARFZ0101`) shares it with its parent share (`IRO3ARFZ0001`), and secondary listings share it with the primary. `CompanyGroup(companyID, date)` returns the group as it stood on a date. All 11 observed rights issues resolved to a parent.

## Measured on the recorded snapshot (trade date 2026-09-23)

| Item | Value |
| --- | --- |
| Instruments | **2205** |
| Parsable ISIN | 2205 (100%) |
| Distinct instrument codes / ISINs / tickers | 2205 / 2205 / 2205 — all unique **on this one date** |
| Ticker collisions on the date | 0 |
| Companies with more than one instrument | 507 |
| Rights issues linked to a parent | 11 of 11 |
| With allowed-range limits (`tmin`/`tmax`) | 2205 |
| With shares outstanding (`z`) and base volume (`bvol`) | 2205 |

The universe required **two requests**: `AllSymbols type=1` returned 1608 equities, funds and rights issues and `type=4` returned 597 debt instruments, with **zero overlap**. No single request returns the whole market, and whether further types exist is unresolved.

## Unresolved identity ambiguity

1. **No listing or delisting dates.** The snapshot carries no listing history, so `listing_date` and `delisting_date` are empty and every instrument's `valid_from` is the snapshot date. Consequence, measured: the quality gate reported `UNKNOWN_INSTRUMENT` for **4643 of 4644** historical instrument-days, because no identity version covers a date before the snapshot. A point-in-time universe is **not yet constructible**; this is the first blocker for survivorship-aware research.
2. **Single-date uniqueness is not identity stability.** Zero ticker collisions on one day says nothing about reuse across years. Detecting that needs dated snapshots over time, or TSETMC symbol-history.
3. **No rename chain.** `previous_ticker` cannot be filled from one snapshot; renames are invisible until dated snapshots accumulate or an official source supplies them.
4. **Mergers and successors.** Nothing in the feed names a successor instrument, so `merged_into` cannot be populated (needs Codal).
5. **Company code is derived, not authoritative.** It comes from the ISIN by a documented rule, not from a provider field. It groups instruments reliably but is not a registry company identifier.
6. **Board and market are coarse.** Only the ISIN board character and `IR` are recorded; the actual board/segment taxonomy is not in the feed.

## Rules

1. Nothing enters the canonical dataset without an `instrument_id`; `Add` rejects a record without one.
2. `ResolveTicker` returns **every** match and the caller must not guess; more than one is a `TICKER_COLLISION` finding.
3. A derived attribute carries `confidence` (`observed` / `derived` / `assumed`). `assumed` must be reviewed before research use.
4. Unresolved questions live in the record's `ambiguities`, never as a silent default.
