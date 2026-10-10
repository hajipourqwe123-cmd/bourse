# Historical Industry Membership (Phase 5B)

Code: `internal/canonical/industry.go`. This table is a prerequisite for any sector-rotation research (family E, H-E1).

## The failure this prevents

Today's industry code is evidence about today. Writing it across an instrument's history asserts that the instrument was always in that sector. Reclassifications, new sub-industries and sector migrations become invisible, and a rotation study then "knows" a membership that was not knowable at the time — look-ahead bias wearing a reference-data costume. The study would also mis-attribute flows: a company reclassified in 2024 would have its 2019 returns credited to the wrong sector.

So membership is **always dated** and `At(id, date)` returns `false` — not a guess — outside the observed window.

## Model

`instrument_id, industry_id, industry_name, valid_from, valid_to, source, confidence`

`Add` rejects a row with no `valid_from`, no source or no confidence. An undated membership is precisely the backfill this table exists to prevent, so it cannot be inserted at all.

`confidence`: `observed` (read from a provider field), `derived` (computed by a documented rule), `assumed` (placeholder, must be reviewed before research use).

## Measured state

| Item | Value |
| --- | --- |
| Distinct industries in the snapshot | **51** (`cs_id`, with `cs` names) |
| Instruments with a dated membership | 2205 |
| `valid_from` for every row | 2026-09-23 (the snapshot's trade date) |
| Membership coverage at 2026-09-23 | **1.0000** |
| Membership coverage at 2026-01-05 | **0.0000** |

That contrast is the whole finding: the recorded snapshot establishes membership for **one date**. There is no historical industry membership at all. Sector research over any earlier period is therefore not currently possible without backfilling, and backfilling is prohibited.

## What would populate it

1. **Dated snapshots accumulating forward.** Each whole-market snapshot adds a dated row; a changed `cs_id` closes the previous interval and opens a new one. This builds real history from the day collection starts, but creates nothing retroactively.
2. **TSETMC official instrument/industry data**, which may carry reclassification history. Unreachable from this network and unassessed.
3. **Codal** for the corporate events behind reclassifications.

Until one of these exists, every sector aggregate must publish `KnownCoverage` for its window, and instruments without a membership on a date are excluded rather than defaulted.

## Rules

1. No undated membership, ever — enforced in code, not by convention.
2. `At` returning false means "unknown", and the caller excludes the instrument from sector aggregates. It must never fall back to the current industry.
3. A sector result without its membership-coverage figure for the window is invalid.
4. Industry ids are the provider's (`cs_id`); they are not assumed stable over time, so a change in an instrument's `cs_id` is recorded as a new interval and never as a correction.
