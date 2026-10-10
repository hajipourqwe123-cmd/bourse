# Data Source Contracts (Phase 5A)

A contract states what a source guarantees, so downstream code can rely on it. Fields not listed are not guaranteed.

## Common rules

- Raw responses are immutable and stored outside Git with sha256 and a manifest entry.
- Every row is point-in-time: `ingest_time` is stored and rows are append-only (spec D1).
- Credentials never enter data, logs, manifests or Git.
- A source silently returning another day's data is a contract violation to be detected, not trusted (C-15).
- Every archived item carries a session-completeness state (`market_session_calendar.md`). Only `POST_SESSION_DAY` and `SESSION_CLOSED_AT_CAPTURE` count as whole-day observations; payload shape never decides completeness.
- Every dataset carries a temporal status. `TEMPORALLY_AMBIGUOUS` means the time at which a value was knowable is not established; such data must not enter a backtest in any way that could leak future information.

## BrsApi `History.php` (daily)

| Item | Contract |
| --- | --- |
| Key | `ins_code`, `date` |
| Fields | `py`, `pf`, `pl`, `pc`, `pmin`, `pmax`, `tvol`, `tval`, `tcnt`; `type=1` adds real/legal volume, value, count |
| Guarantees | one row per calendar trading day incl. halted days (`tvol = 0`, `pmin = pmax = 0`) |
| Not guaranteed | `pc` inside [`pmin`,`pmax`] (285 of 4230 traded days in the Foolad sample); consistent int/float types; real+legal = total (12 exceptions); delisted symbols; allowed-range limits |
| Limit | Q-SG1 OPEN |
| Consumers | `internal/history` (state, fills, adjustment) |

## tablokhani `big-movers/matrix?date=` (10-minute hot-money matrix)

| Item | Contract |
| --- | --- |
| Key | `date`, `symbol` (Persian ticker; no `ins_code`), interval label. Joining to `ins_code`-keyed data needs a dated ticker map (corporate_action_policy rule 1) |
| Fields | `hotMoneyValues`, `priceChangeValues` (percent per interval), `priceLevelValues`, `hotMoneyTotal`, `columnTotals`, `meta.snapshot_created_at` |
| Guarantees | payload `date` echoes the served day |
| Temporal status | `INTERVAL_STAMPED`: values are labelled by a 10-minute interval of the market day. The archive holds the values as served on 2026-10-05 (`api_generated_at`), so later provider revisions cannot be detected |
| Not guaranteed | price or volume levels (only percent changes and hot-money values); a fixed interval grid (20-21 labels to 12:30 until July 2026, 36 to 15:00 from 2026-08-01); every interval of a day being present |
| Violations seen | request for a no-data day returns another day (102 of 248 items, 32 of them Sat-Wed); low-coverage days (~490-563 symbols); 18 days with fewer intervals than neighbouring days (as few as 2) |
| Validation | `payload_date == requested`; symbols >= 0.7 x median; interval count >= median of genuine days within ±15 days (`partial_interval_grid`); session completeness |

## tablokhani `symbol-scores?score_date=&end_date=`

| Item | Contract |
| --- | --- |
| Key | `score_date`, `symbol` (Persian ticker; no `ins_code`) |
| Fields | component scores, `total_score`, `buy_per_capita`, `buy_to_sell_ratio`, `volume_ratio`, `created_at` (UTC). `sell_per_capita`, `real_buy_volume`, `real_sell_volume`, `trade_volume` are null in 99.4% of rows; `co_buy_volume`, `co_sell_volume` in 100% |
| Temporal status | **`TEMPORALLY_AMBIGUOUS`**. `created_at` (one per row, no update time) falls on day D between 09:00 and 12:30 Tehran for 90.7% of rows, after 12:30 on day D for 3.6%, before 09:00 on day D for 0.3%, and on the day before D for 5.4%. A day-D score may encode day D-1 information, day-D opening information or day-D intraday information, and the mix differs by row. Not usable in a backtest until aligned with independent price/volume data |
| Not guaranteed | final value after the close; component scale; `volume_ratio` label (holds volume) |
| Validation | every row's `score_date` equals the request; no duplicate symbols; session completeness |

## SourceArena / TSETMC

Contracts are written after the benchmark (provider_benchmark_plan.md). TSETMC remains unassessed.

## Archive manifest schema

`dataset, retrieved_at, market_date, symbol_if_applicable, endpoint_identifier, request_parameters, http_status, record_count, content_length, sha256, schema_version` (+ `file`, `payload_date`). Code: `research/tkarchive/store.go`.
