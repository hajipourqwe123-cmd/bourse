# CLAUDE.md — project memory for Claude Code (keep short: every line is loaded every session)

Iranian stock-market real-time analytics platform (rival to tablokhani.com). Source of truth for
requirements: the PRD (Claude Doc, Persian). Human docs are Persian in docs/; code and comments English.

## Commands
- `make test` (= `go test ./...`), `make vet`, `go test -race ./...`, `gofmt -l .` must be empty
- `make synth` then `make demo` → replays a SYNTHETIC day through collector | engine → out.ndjson
- `make env` (once) → `make up` → `make ddl`: local stack (infra/, ports on 127.0.0.1 only)

## Map
- internal/model     canonical Snapshot + event types (rial int64, source_time vs ingest_time, Missing[])
- internal/quality   data-quality rules (docs/data-quality.md)
- internal/flow      hot money / hot-plus / market game / 10-min matrix (ADR-0004)
- internal/anomaly   AI tier 1: per-symbol EWMA anomaly radar (AI-01), flow–price divergence (AI-02)
- internal/source    adapters: replay (done), sourcearena (PROVISIONAL, docs/source-mapping.md)
- internal/bus       Publisher interface + NDJSON; subjects in contracts/subjects.md
- cmd/{collector,engine,syngen}; infra/ (compose, ClickHouse DDL, Centrifugo)

## Non-negotiable rules
1. Missing/inconsistent data → no metric + a QualityIssue. Never zero-fill, never estimate.
2. Money in rial int64. Tehran time via internal/tehran. Keep source and ingest time separate.
3. No secrets in code, tests, logs or URLs in errors (see redact()). Secrets only via env.
4. Every user-facing metric has a published formula and unit tests; AI signals carry a Reason string.
5. SYN* synthetic data never reaches users or backtests.
6. Do not copy tablokhani's proprietary names/formulas.
7. Iran-hosted production: no runtime dependency on services unavailable in Iran (see ADR-0005).

## Working agreement
- One sprint task per session (IDs in docs/phase1-plan.md); `/clear` between tasks.
- Plan mode for anything touching more than 3 files; state the task ID in the first message.
- Done = tests for every formula + vet/race/gofmt green + docs updated if behaviour changed.
- Do not Read testdata/*.ndjson or recordings/ (large); use `head -c` or jq summaries instead.

## Current state
Sprint 0 done. Next: Sprint 1 (I-01 compose up, D-03 verify sourcearena fields with live token,
P-01 NATS publisher, W-01 ClickHouse writer, R-01 daily recording, Q-01 Grafana, Q-02 hot-money accuracy).
