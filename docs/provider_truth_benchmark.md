# Provider Truth Benchmark (Phase 5B)

Code: `internal/canonical/benchmark.go`. Run: `cmd/canonical` on recorded responses. Complements `provider_benchmark_plan.md` (which covers intraday/live metrics).

## Method

For the same `instrument_id` and `trade_date`, compare every field both providers publish: last, closing, high, low, first, volume, value, trade count, and the real/legal flows. Report per field:

`compared, missing_a, missing_b, exact_matches, exact_match_rate, mean_rel_error, max_rel_error, mean_signed_error (systematic bias), bias_direction, worst_date`

Plus, per pair: overlap dates, only-in-A, only-in-B, missing rates, and each provider's date range.

Three rules keep the numbers honest:

1. **Two providers disagreeing does not say which is right.** It bounds how much trust a single one deserves and locates the fields to investigate.
2. **A field one provider does not publish is missing, never agreement or disagreement.** Absence is declared per row in `absent_fields`.
3. **The canonical source is not chosen for having the most fields.** It is chosen on identifier stability, timestamp semantics, historical depth and coverage.

## Reachability (measured 2026-10-08/09, this network)

| Provider | HTTPS 443 | HTTP 80 | Note |
| --- | --- | --- | --- |
| TSETMC (`tsetmc.com`, `cdn.`, `old.`, `www.`) | timeout | timeout | DNS resolves; TCP never completes |
| SourceArena (`api.sourcearena.ir`) | timeout | timeout | — |
| **BrsApi** (`api.brsapi.ir`) | timeout | **200, live API** | `History.php` returns a structured `missing_param` error for `key` |

BrsApi is reachable, but **only unencrypted**. Its key travels as a URL query parameter, so using it over HTTP exposes the key to anyone on the path and to proxy logs. Phase 5B therefore ran **design-only** by owner decision: no key, no live request. This overturns MI-01b's "BrsApi unreachable" conclusion, which was based on HTTPS alone.

## Result: BrsApi vs SourceArena (Foolad, 22 overlapping days)

| Field | Compared | Exact match | Mean rel. error | Bias |
| --- | --- | --- | --- | --- |
| `last_price` | 22 | **1.0000** | 0.000000 | none |
| `high` | 22 | **1.0000** | 0.000000 | none |
| `low` | 22 | **1.0000** | 0.000000 | none |
| `first_price` | 22 | **1.0000** | 0.000000 | none |
| `volume` | 22 | **1.0000** | 0.000000 | none |
| `closing_price` | 0 | — | — | not published by SourceArena |
| `value`, `trade_count`, real/legal flows (4) | 0 | — | — | not published by SourceArena |

Overlap 22 days; only-in-A 4622; only-in-B 0.

**On every field both providers publish, they agree exactly.** Their raw observations of traded prices and volume are consistent.

### The finding that matters: a mislabelled field

A first pass compared SourceArena's `close_price` against BrsApi's `closing_price` and reported a **0.1818 exact-match rate** with a systematic bias. That was a semantic trap, not a data defect. Against the same window:

- SourceArena `close_price` == BrsApi `pl` (**last traded price**) in **22 of 22** rows.
- SourceArena `close_price` == BrsApi `pc` (**official closing price**) in only 4 — exactly the days where last and close coincide.

SourceArena's "close_price" is the **last traded price**, and SourceArena publishes **no** official closing price. The canonical mapping now sends it to `last_price` and declares `closing_price` absent for that provider.

Had the field been mapped by name, every close-based analytic would have been silently wrong, and ironically would have been using a *more* executable price than the label implied. This is the concrete reason a name-based merge of providers is prohibited, and why ADR-0006 field ownership is enforced.

## Provider evaluation

| Metric | BrsApi | SourceArena | TSETMC official |
| --- | --- | --- | --- |
| Reachable here | HTTP only (unencrypted) | no | no |
| Historical depth | **19.54 years** (4644 rows, 2007-03-11..2026-09-23, one sampled instrument) | **22 days** in the recording | OPEN |
| Whole-market universe | 2205 instruments, but needs 2 requests (`type=1` 1608 + `type=4` 597, **disjoint**); completeness of the type set unknown | not assessed | OPEN |
| Identifier stability | instrument code + ISIN + industry + base volume + price limits | ticker only in the recorded history | OPEN |
| Timestamp semantics | Jalali date + last-trade `time`; daily date reliable | Jalali date only | OPEN |
| Schema stability | mixed int/float for the same field | **same field quoted on some rows, bare on others, in one response** | OPEN |
| Corporate-action handling | none; `py != pc(t-1)` discontinuities must be derived (32 in 19.5 years) | none | OPEN |
| Real/legal fields | yes (`type=1`, from 2008) | **no** | OPEN |
| Missingness | halted days present as zero-volume rows; 6 rows dated Thu/Fri with zero volume | — | OPEN |
| Rate limit | **Q-SG1 still OPEN**; only `AllSymbols` limits ever seen (100/day, 300/5min) | about 45/day observed earlier | OPEN |
| Cost | free plan | free quota | free |
| Failure behavior | structured JSON error with an `account` block | — | OPEN |

## Conclusion

**BrsApi is the only candidate for the canonical daily dataset.** Not because it has more fields, but because it is the only reachable source with multi-year depth, stable instrument identifiers, and the real/legal breakdown the research plan needs. SourceArena's recorded depth (22 days) disqualifies it for history and it publishes no official close; TSETMC stays unassessed and remains the preferred long-term source if an Iran-network path appears.

Two conditions must be settled before BrsApi is adopted as canonical:

1. **Transport.** HTTPS is blocked here; using HTTP exposes the API key. Either an encrypted route or an explicit, informed acceptance of that exposure.
2. **Rate limit (Q-SG1).** The `History.php` limit has never appeared in any response. The first live step stays the single invalid-parameter request that returns the `account` block without consuming data.

## Open questions

- Is the `AllSymbols` type set complete at `{1, 4}`, or are there further disjoint universes?
- Does BrsApi revise historical rows after publication? The recordings are single-shot, so revisions are undetectable; the download plan therefore keeps every raw response.
- Does SourceArena's history extend beyond 22 days on request, and does it ever publish an official close?
