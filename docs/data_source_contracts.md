# Data Source Contracts (Phase 5A)

A contract states what a source guarantees, so downstream code can rely on it. Fields not listed are not guaranteed.

## Common rules

- Raw responses are immutable and stored outside Git with sha256 and a manifest entry.
- Every row is point-in-time: `ingest_time` is stored and rows are append-only (spec D1).
- Credentials never enter data, logs, manifests or Git.
- A source silently returning another day's data is a contract violation to be detected, not trusted (C-15).

## BrsApi `History.php` (daily)

| Item | Contract |
| --- | --- |
| Key | `ins_code`, `date` |
| Fields | `py`, `pf`, `pl`, `pc`, `pmin`, `pmax`, `tvol`, `tval`, `tcnt`; `type=1` adds real/legal volume, value, count |
| Guarantees | one row per calendar trading day incl. halted days (`tvol = 0`, `pmin = pmax = 0`) |
| Not guaranteed | `pc` inside [`pmin`,`pmax`]; consistent int/float types; real+legal = total (12 exceptions); delisted symbols; allowed-range limits |
| Limit | Q-SG1 OPEN |
| Consumers | `internal/history` (state, fills, adjustment) |

## tablokhani `big-movers/matrix?date=` (10-minute hot-money matrix)

| Item | Contract |
| --- | --- |
| Key | `date`, `symbol`, interval label |
| Fields | `hotMoneyValues`, `priceChangeValues` (percent per interval), `priceLevelValues`, `hotMoneyTotal`, `columnTotals`, `meta.snapshot_created_at` |
| Guarantees | payload `date` echoes the served day |
| Violations seen | request for a no-data day returns another day; low-coverage days (~515 symbols); interval set changed in August 2026 |
| Validation | `payload_date == requested`, symbols >= 0.7 x median, interval count recorded |

## tablokhani `symbol-scores?score_date=&end_date=`

| Item | Contract |
| --- | --- |
| Key | `score_date`, `symbol` |
| Fields | component scores, `total_score`, per-capita, real volumes, `trade_volume`, `created_at` |
| Not guaranteed | final value after the close (`created_at` is the last in-session update); component scale; `volume_ratio` label (holds volume) |
| Validation | every row's `score_date` equals the request; no duplicate symbols |

## SourceArena / TSETMC

Contracts are written after the benchmark (provider_benchmark_plan.md). TSETMC remains unassessed.

## Archive manifest schema

`dataset, retrieved_at, market_date, symbol_if_applicable, endpoint_identifier, request_parameters, http_status, record_count, content_length, sha256, schema_version` (+ `file`, `payload_date`). Code: `research/tkarchive/store.go`.
