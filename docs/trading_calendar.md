# Trading Calendar (Phase 5B)

Code: `internal/canonical/trading_calendar.go`. This answers **"was the market open on this date"**. It is a different question from `internal/calendar` and `docs/market_session_calendar.md`, which answer **"when within a day may this instrument class trade"**. Neither substitutes for the other.

## A weekday pattern is not evidence

Saturday to Wednesday is the ordinary Iranian trading week, but public holidays fall on those days and the repo's holiday list is empty and unverified. So a Saturday is not a trading day until something shows it was, and a Thursday is not provably closed either.

What *is* evidence: **a trade cannot happen on a closed market.** If any instrument printed a trade on a date, the market was open — no holiday list required. This makes the calendar derivable from whole-market data rather than asserted.

## Model

`trade_date, is_trading_day, closure_type, holiday_reason, source, verified, instruments_traded`

| `closure_type` | Meaning | `is_trading_day` | `verified` |
| --- | --- | --- | --- |
| `OBSERVED_TRADING` | at least one instrument printed a trade | true | **true** (observation) |
| `CONFIRMED_CLOSURE` | an independent verified source states the market was shut | false | true |
| `POSSIBLE_MARKET_CLOSURE` | no source has data and no instrument traded | false | false |
| `WEEKEND_BY_RULE` | Thu/Fri under the weekly rule, nothing traded | false | false |
| `PROVIDER_OUTAGE` | the market traded but this provider has no data | true | true |
| `UNDETERMINED` | no evidence either way | false | false |

## Rules

1. **Observed trades win.** `ObserveTrading` promotes a date and later "no data" reports cannot downgrade it. A provider's silence never overrides another's trades.
2. **An archive gap is never a holiday.** Synchronized gaps across sources are `POSSIBLE_MARKET_CLOSURE` with `holiday_reason` left as "reason not established". Only `ConfirmClosure`, from a verified independent source, produces `CONFIRMED_CLOSURE`, and only then is a reason named.
3. **One instrument's halt is not a market closure.** A zero-volume row means that instrument did not trade; the market may well have been open. Such dates stay `UNDETERMINED`. The proof run follows this: it records only traded days and claims **zero** closures from a single-instrument history.
4. **A provider missing a day on which trading was observed is a provider defect**, recorded as `PROVIDER_OUTAGE` against that provider, not as a market event.
5. `holiday_reason` is populated only for verified closures. An unverified date keeps the standing text that the reason is not established.

## Measured evidence (one instrument, 2007-03-11 to 2026-09-23)

| Item | Value |
| --- | --- |
| Dates proven to be trading days | **4230** |
| Closures claimed | **0** (a single instrument cannot establish them) |
| Rows dated Thursday or Friday | 6 |
| Of those, rows that traded | **0** |
| Traded days by weekday | Sat 839, Sun 852, Mon 850, Tue 848, Wed 841 — **none** on Thu/Fri |

All 4230 observed trading days fall Saturday to Wednesday. That is real support for the weekly rule, from data rather than assumption, but it comes from one instrument: it cannot show which Sat-Wed dates were holidays, because this instrument's halts are indistinguishable from market closures without other instruments.

The 6 Thursday/Friday rows carry zero volume. The provider emits rows on dates the market did not trade, so **the presence of a row is not evidence of a trading day** — only a trade is.

## What completing this calendar requires

1. **A whole-market daily load.** With about 2205 instruments, any open date has thousands of trades, so `OBSERVED_TRADING` becomes conclusive and the remaining no-trade dates become a short, inspectable candidate-holiday list.
2. **An official holiday source** to turn those candidates into `CONFIRMED_CLOSURE` with reasons. Until then they stay unverified, and the MI-01b synchronized gaps (12 Sat-Wed dates in 2026 with no data in either tablokhani dataset) remain `POSSIBLE_MARKET_CLOSURE`.
3. **Dated session rules** (`market_session_calendar.md`), which are a separate, still-unverified input.
