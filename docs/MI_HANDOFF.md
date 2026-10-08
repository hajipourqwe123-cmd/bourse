# MI-01b Handoff (Market Intelligence Research)

Status 2026-10-08. Branch `claude/market-intelligence-research`, no PR. Pattern Discovery, ML, ranking and signals have **not** started.

## Where things are

| Item | Location |
| --- | --- |
| Raw tablokhani archive (outside Git) | `D:\Bourse\data\tablokhani_archive\` (`hot_money_matrix\`, `symbol_score_history\`, `manifests\`) |
| Manifests (append-only JSONL, latest line per key wins) | `manifests\hot_money_matrix.jsonl`, `manifests\symbol_score_history.jsonl` |
| Validation report | `manifests\validation_report.json` (machine), `manifests\validation_report.md` (human) |
| Reconciliation | `manifests\reconciliation.json`; last ingest run `manifests\ingest_report.json` |
| Archive tooling | `research/tkarchive` (store, ingest, annotate, validate, session completeness); binary `D:\Bourse\data\tkarchive-ingest.exe` |
| Findings | `docs/archive_feasibility.md` section 8 |
| Phase 5A policies | `trading_state_model.md`, `executable_price_policy.md`, `corporate_action_policy.md`, `survivorship_policy.md`, `intraday_sampling_policy.md`, `provider_benchmark_plan.md`, `data_source_contracts.md`, `market_session_calendar.md` |
| Daily-history semantics code | `internal/history` (state, executable price, adjustment) |

## Archive facts (recomputed from manifests)

- 521 planned items (matrix 2026-01-31..2026-10-04, scores 2026-01-04..2026-10-04), all present, checksum-valid; 0 duplicates, 0 corrupt, 0 quarantined, 0 errors. 42 were archived before the main ingest, 479 in it.
- Two extra same-day captures of 2026-10-05 exist, labelled `same_day_capture` / `SESSION_UNKNOWN`. **No post-close capture of 2026-10-05 exists**; the subscription ended 2026-10-06.
- Usable backtest days: matrix 105 of 178 expected trading days; scores 0 (`TEMPORALLY_AMBIGUOUS`).

## Commands

```bash
D:/Bourse/data/tkarchive-ingest.exe ingest -root 'D:\Bourse\data\tablokhani_archive'
D:/Bourse/data/tkarchive-ingest.exe validate -root 'D:\Bourse\data\tablokhani_archive' -from 2026-01-04 -to 2026-10-05
D:/Bourse/data/tkarchive-ingest.exe annotate -root 'D:\Bourse\data\tablokhani_archive' -dataset hot_money_matrix -key 2026-10-05 -kind same_day_capture -completeness SESSION_UNKNOWN
go test -p 1 ./...
```

Rebuild the binary after changing `research/tkarchive`: `go build -o D:/Bourse/data/tkarchive-ingest.exe ./research/tkarchive/cmd/tkarchive`.

## Rules carried forward

1. Raw files are immutable; corrections are new manifest lines, never edits. Nothing is forward-filled.
2. Completeness is session-aware by instrument class (`market_session_calendar.md`); no interval count means "end of day".
3. Symbol Score History stays `TEMPORALLY_AMBIGUOUS` and out of backtests until aligned with independent price/volume data.
4. A halted row is never a lock; `closing_price` is never a fill price; fills, MFE and MAE use traded prices; UNKNOWN when evidence is insufficient.
5. Credentials never enter files, logs, manifests or Git. The secret scan quarantines real credentials and records false positives without printing values.

## Blockers before Pattern Discovery

1. No canonical daily price/volume series for the universe: BrsApi unreachable from this network; only one sample symbol on disk; History limit Q-SG1 open.
2. Length: matrix 105 and scores 184 genuine days against about 1100 needed for G-3a.
3. Scores temporal alignment unresolved; most score inputs (sell per capita, real volumes, trade volume) are null.
4. Survivorship: no delisted-name source before 2026; tablokhani archives key by Persian ticker, not `ins_code`.
5. Session calendar and holiday list unverified; instrument-class map provisional.
6. Provider limits and intraday provider choice open (provider_benchmark_plan.md); TSETMC unassessed.

## Open questions for the owner

- Verify session hours per class and the 2026 holiday list against official notices (unblocks `SESSION_CLOSED_AT_CAPTURE` and separates closures from outages).
- Network path to BrsApi / TSETMC (Iran-hosted machine) for the universe history load.
- Whether to renew the tablokhani subscription (it would allow a post-hoc recapture of 2026-10-05 and forward collection; not needed for the current conclusions).
