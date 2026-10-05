# Provider Benchmark Plan (Phase 5A)

Investigation priority: 1. TSETMC official / Iran-network access, 2. BrsApi, 3. SourceArena. Values below are what is **known**; unknown stays `OPEN`. No value is invented.

## Metrics

coverage, latency, timestamp_accuracy, missing_rate, schema_stability, rate_limit, historical_depth, available_fields, failure_rate, cost.

## Current evidence

| Metric | TSETMC official | BrsApi | SourceArena |
| --- | --- | --- | --- |
| historical_depth | OPEN (not reachable from this machine; hypothesis: per-day intraday history exists) | daily from 2007 (one sample symbol); no intraday | intraday only for the current day |
| available_fields | OPEN | OHLC, volume, value, count, real/legal daily (`type=1`), order book live | real/legal, per-capita, queues, book |
| rate_limit | OPEN | **Q-SG1 OPEN** for `History.php`. Seen only: free plan `usage_today_limit=100` and `usage_5min_limit=300` on `AllSymbols`. Not a verified History limit | unverified (about 45/day observed in earlier probes) |
| timestamp_accuracy | OPEN | daily date reliable; `time` = last-trade time | `source_time_estimated=true` in our recordings |
| schema_stability | OPEN | `pc`/percent fields mixed int/float | stable in samples |
| latency / failure_rate / missing_rate | OPEN | measured only after network access returns | measured in our own recordings only |
| cost | free | free plan limited; paid plans unverified | free quota limited |

## Procedure (when network access allows)

1. One invalid-parameter request per provider endpoint to read the account/limit block without consuming data.
2. A fixed 20-symbol panel for 5 trading days at the target interval; record latency, timestamps, gaps, schema hash per response.
3. Compare against daily BrsApi history for price/volume consistency.
4. Fill the table; rate limits go to config only after being observed in a provider response or confirmed in the vendor panel.

## Output

A ranked provider chain per tier (intraday_sampling_policy.md), with evidence links for every number.
