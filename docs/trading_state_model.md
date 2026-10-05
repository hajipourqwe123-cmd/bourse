# Trading State Model (Phase 5A)

Implementation: `internal/history/state.go`. Tests: `internal/history/history_test.go`.

## Correction to the earlier assumption

The earlier spec (U1, U4, D5) assumed a halted day has no BrsApi row. **False.** `History.php` returns a row for a halted day with `tvol = 0`, `pmin = pmax = 0`, `pc = py` (Foolad sample: 414 such rows, 88 in 1405). Consequences already fixed here:

- A zero-volume, zero-range row is **never** a price-limit lock (`volume == 0 && high == 0 && low == 0` is not `pmin == pmax`).
- U4 (halt detection) must read states, not "days missing from the calendar".

## States

| State | Meaning |
| --- | --- |
| `TRADING` | trades printed over a price range |
| `HALTED` | no trades, base price unchanged (or source says `halted`) |
| `SUSPENDED` | halt run of at least `SuspendedMinRun` rows (default 10, configurable policy, not a vendor fact) or source says `suspended` |
| `REOPENED` | first traded row after HALTED/SUSPENDED (range rules differ on reopening, so locks are not evaluated that day) |
| `LIMIT_UP_LOCKED` / `LIMIT_DOWN_LOCKED` | all trades at one price at (known) or toward (proxy) the range limit |
| `UNKNOWN` | evidence insufficient or contradictory |

Every result carries `Evidence`: `explicit` (source status or known limit price), `inferred` (OHLCV only), `none` (UNKNOWN).

## Classification rules (in order)

1. `SourceStatus` = `suspended` / `halted` -> that state, explicit.
2. `volume == 0`:
   - non-zero high/low, negative volume, or `pc != py` -> `UNKNOWN`;
   - else run >= `SuspendedMinRun` -> `SUSPENDED` (inferred); otherwise `HALTED` (inferred).
3. `volume > 0` but `high <= 0`, `low <= 0`, or `high < low` -> `UNKNOWN`.
4. Previous state HALTED/SUSPENDED -> `REOPENED`.
5. `low == high`:
   - known `UpperLimit`/`LowerLimit` equal to the traded price -> lock (explicit); single price away from known limits -> `TRADING`;
   - limits unknown (proxy D5): traded price above `py` -> `LIMIT_UP_LOCKED`, below -> `LIMIT_DOWN_LOCKED` (inferred); equal to `py` -> `UNKNOWN`.
   - Direction uses the **traded price**, not `pc`, because `pc` can lie outside every trade (see executable_price_policy.md).
6. otherwise `TRADING`.

## Known weakness of the proxy

A very thin one-trade day away from the limit looks like a lock under the proxy. Hence `Evidence = inferred`, and the range table (DL-03d) is required before lock-dependent results are reported as exact.
