# Historical Download Plan (Phase 5B, Track A)

Target: whole-market daily history at **maximum available depth** (owner decision, 2026-10-09). The sampled instrument reaches 2007-03-11, about 19.5 years, so the plan is sized for full depth per instrument rather than a fixed cut-off, and reports achieved depth per instrument.

No code in this plan has been run live. Phase 5B is design-only: BrsApi is reachable over unencrypted HTTP only, and the transport decision is unresolved (`provider_truth_benchmark.md`).

## Properties (required, all enforced in the loader)

| Property | Mechanism |
| --- | --- |
| **Resumable** | per-instrument manifest; a completed instrument is skipped on restart |
| **Idempotent** | raw response keyed by `(provider, endpoint, params, retrieved_at)`; re-running changes nothing already stored |
| **Rate-limited** | persisted daily counter on disk, min interval between requests, exponential backoff with jitter; corrupt counter file = **fail closed** |
| **Cache-aware** | one request returns an instrument's whole history, so each instrument is fetched once per load; cached responses are reused |
| **Checksum-verified** | sha256 per raw file, verified before normalization; a mismatch quarantines the file and never silently re-downloads over it |
| **Provider-version-aware** | the response's schema fingerprint and endpoint version are stored per file; a change raises `SCHEMA_DRIFT` instead of being absorbed |
| **Immutable raw** | `O_EXCL` write, read-only mode, never overwritten — same contract as the MI-01b archive |

Raw data lives outside Git (`D:\Bourse\data\` tree). Credentials never enter files, logs, manifests, URLs-in-logs or Git; the key is read from the environment and never printed.

## Request budget

| Step | Requests | Notes |
| --- | --- | --- |
| Universe snapshot | 2 per snapshot day | `AllSymbols type=1` + `type=4`; they are disjoint and both are needed |
| Daily history `type=0` | 1 per instrument | whole history per request |
| Real/legal history `type=1` | 1 per instrument | whole history per request |
| **Full load** | **≈ 4412** (2205 instruments × 2) | plus 2 for the universe |

At the only limit ever observed in a response (100/day on `AllSymbols`), a full load would take weeks. **The `History.php` limit (Q-SG1) has never appeared in any response and must not be guessed.** The loader therefore refuses to start without an explicitly configured limit.

## Configuration

| Key | Default | Behavior |
| --- | --- | --- |
| `BRSAPI_HISTORY_DAILY_LIMIT` | **none** | mandatory; the loader does not start without it |
| `BRSAPI_HISTORY_MIN_INTERVAL` | 10s (policy, not a vendor fact) | minimum gap between History requests |
| `BRSAPI_HISTORY_BACKOFF` | 30s, ×2, cap 30m, jittered | network errors, 5xx, non-JSON bodies |
| `Retry-After` | honored if present | never yet observed in a response |
| quota error (`account` full or `request_block != 0`) | stop until the next Tehran day | existing `ErrBudget` behavior |
| counter | persisted file on D: | survives restarts; fail closed if unreadable |

## Order of work

1. **Single probe.** One `History.php` request with an invalid parameter, to read the `account` block and learn the real limit without consuming data. Record whether the probe itself counts against quota.
2. **Universe snapshot** (`type=1` + `type=4`), stored raw. This dates a security-master version and an industry-membership interval.
3. **Pilot of 20 instruments** across classes, to measure per-request latency, payload size, schema fingerprints and failure modes before committing to the full load.
4. **Full load**, resumable, newest-listed first so a partial load is still usable.
5. **Normalize** into canonical rows, classify states, run the quality gates, write the coverage report.
6. **Re-snapshot the universe on a schedule.** This is the only mechanism that builds dated identity and industry history (renames, delistings, reclassifications), none of which can be recovered retroactively. Every day without a snapshot is history permanently lost.

## Known limits of this plan

- It cannot recover **listing/delisting dates**, **rename chains** or **historical industry membership**: the feed has none. Those come from forward snapshots, TSETMC or Codal.
- It cannot recover **historical shares outstanding** or **base volume**; History omits both.
- It cannot detect **provider revisions** of past rows from a single load; keeping every raw response is what makes later detection possible.
- A whole-market load is the prerequisite for the trading calendar (`trading_calendar.md`) and for any survivorship measurement by identity rather than by ticker label.
