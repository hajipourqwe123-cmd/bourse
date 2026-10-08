# Market Session Calendar and Session Completeness (Phase 5A)

Sessions are a property of the **instrument class and the date**, not of the market as a whole and not of any provider's payload. The session data is `internal/calendar/sessions.json` (format and editing rules: `docs/sessions.md`). This document defines how an observation's completeness is judged against it. Code: `research/tkarchive/session.go`.

## Sessions by class

| Class | Trading window (Tehran) | Status |
| --- | --- | --- |
| `stock`, `equity_etf`, `other_fund` | 09:00-12:30 | unverified (owner-provided 2026-09-25) |
| `fixed_income` | 08:30-15:00 | unverified |
| `gold`, `silver` | 12:00-18:00 | unverified |
| `unknown` (unmapped) | union of all classes: 08:30-18:00 | default |

Rules are dated (`effective_from`), so a replay uses the calendar of that day. The holiday list is empty and unverified. Instrument-to-class mapping exists only provisionally (`cmd/classmap`).

## Completeness states

| State | Meaning | Counts as a whole-day observation |
| --- | --- | --- |
| `POST_SESSION_DAY` | captured on a later Tehran day; every class's session for that day has ended (sessions never cross midnight) | yes, but provider finality is still not proven |
| `SESSION_CLOSED_AT_CAPTURE` | same-day capture after the class's **verified** close | yes |
| `PARTIAL_INTRADAY` | same-day capture before the class's **verified** close | no |
| `SESSION_UNKNOWN` | the session cannot be established: class unmapped and the union is unverified, rule unverified, no rule for the day, capture time unknown, or capture before the market day | no |

## Rules

1. Completeness comes only from the capture time, the market date, the class and the calendar. **No payload shape is a completeness signal**: not an interval count, not a last interval label, not "36 intervals through 15:00".
2. A multi-class dataset without a per-row class (both tablokhani datasets) uses class `unknown`. Its union session ends at 18:00 and is unverified, so a same-day capture is `SESSION_UNKNOWN`.
3. While every calendar rule is unverified, no same-day capture can be `SESSION_CLOSED_AT_CAPTURE`. Verifying rules against official exchange notices is the precondition.
4. A non-closed observation is kept unchanged in the raw store, labelled, and excluded from day-level coverage and backtests. It is never replaced by, or merged with, a later capture of the same day.
5. A later capture of the same day is a separate immutable item (`<date>@recapture`). It is assessed with the same rules and used only when it is session-complete and the original is not.
6. An explicit manifest annotation (`completeness`) overrides the derived state. Annotations are appended; earlier manifest lines stay as history.
7. Provider-side gaps inside a day (missing interval labels compared with the provider's neighbouring days) are a data-quality class (`partial_interval_grid`), not a session state.

## Evidence that a fixed interval count is not a completeness rule

- The matrix grid changed from 20-21 labels ending 12:30 (to July 2026) to 36 labels ending 15:00 (from 2026-08-01). In July the provider's `data_last_seen_at` was about 16:00 while the grid stopped at 12:30, so the grid is neither the session nor the provider's update schedule.
- On 2026-10-04, 589 of 1111 symbols, ordinary stocks included, change `priceLevelValues` after 12:30. Either the stock session ran past the calendar's 12:30, or the provider keeps updating values after the close. The archive cannot tell which.
- Classes close at 12:30, 15:00 and 18:00. A 15:00 cut-off would mark gold and silver days complete three hours early.

## Applied to the archive (2026-10-08)

| Item | Capture | State |
| --- | --- | --- |
| 521 planned items (2026-01-04..2026-10-04) | downloaded 2026-10-05 | `POST_SESSION_DAY` |
| `hot_money_matrix` 2026-10-05 | 2026-10-05 13:46 Tehran (`snapshot_created_at` 13:45:05, 29 labels to 13:50) | `SESSION_UNKNOWN` (annotated; observation kind `same_day_capture`) |
| `symbol_score_history` 2026-10-05 | same | `SESSION_UNKNOWN` (annotated) |

No later capture of 2026-10-05 exists.
