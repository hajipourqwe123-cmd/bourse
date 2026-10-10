package canonical

import (
	"fmt"
	"sort"
)

// Quality gates (docs/canonical_quality_gates.md). Every gate DETECTS; none
// repairs. A detected problem becomes a quality code on the row and a counted
// finding in the report. Repairs happen only in the normalized layer, are
// recorded as separate rows with their own provenance, and never overwrite the
// raw observation.
const (
	QWrongServedDate     = "WRONG_SERVED_DATE"
	QMissingTradingDay   = "MISSING_TRADING_DAY"
	QDuplicateObs        = "DUPLICATE_OBSERVATION"
	QImpossibleOHLC      = "IMPOSSIBLE_OHLC"
	QNegativeVolumeValue = "NEGATIVE_VOLUME_OR_VALUE"
	QUnknownInstrument   = "UNKNOWN_INSTRUMENT"
	QTickerCollision     = "TICKER_COLLISION"
	QActionDiscontinuity = "CORPORATE_ACTION_DISCONTINUITY"
	QHaltMisclassified   = "HALT_ROW_MISCLASSIFIED"
	QSchemaDrift         = "SCHEMA_DRIFT"
	QTimestampAmbiguous  = "TIMESTAMP_AMBIGUITY"
	QCloseOutsideRange   = "CLOSING_PRICE_OUTSIDE_TRADED_RANGE"
	QFlowExceedsTotal    = "REAL_LEGAL_EXCEEDS_TOTAL"
	QMissingRealLegal    = "MISSING_REAL_LEGAL"
	QMissingPointInTime  = "MISSING_POINT_IN_TIME_ATTRIBUTES"
)

// Finding is one detected data-quality problem.
type Finding struct {
	Code         string `json:"code"`
	InstrumentID string `json:"instrument_id,omitempty"`
	TradeDate    string `json:"trade_date,omitempty"`
	Detail       string `json:"detail"`
}

// QualityReport is the output of a gate run.
type QualityReport struct {
	BarsChecked int            `json:"bars_checked"`
	Counts      map[string]int `json:"counts"`
	Findings    []Finding      `json:"findings"`
	// Truncated is true when Findings was capped; Counts stays complete.
	Truncated bool `json:"findings_truncated"`
}

const maxFindings = 500

func (q *QualityReport) add(code, instrument, date, detail string) {
	if q.Counts == nil {
		q.Counts = map[string]int{}
	}
	q.Counts[code]++
	if len(q.Findings) < maxFindings {
		q.Findings = append(q.Findings, Finding{code, instrument, date, detail})
	} else {
		q.Truncated = true
	}
}

// CheckBars runs every row-level and series-level gate over one instrument's
// bars (oldest first). master and cal may be nil to skip the gates that need
// them. Detected codes are appended to each bar's QualityCodes.
func CheckBars(bars []DailyBar, master *Master, cal *TradingCalendar) *QualityReport {
	q := &QualityReport{Counts: map[string]int{}}
	seen := map[string]int{}
	for i := range bars {
		b := &bars[i]
		q.BarsChecked++

		if b.ServedDate != "" && b.RequestedDate != "" && b.ServedDate != b.RequestedDate {
			b.QualityCodes = append(b.QualityCodes, QWrongServedDate)
			q.add(QWrongServedDate, b.InstrumentID, b.RequestedDate,
				fmt.Sprintf("requested %s, served %s", b.RequestedDate, b.ServedDate))
		}
		if n := seen[b.TradeDate]; n > 0 {
			b.QualityCodes = append(b.QualityCodes, QDuplicateObs)
			q.add(QDuplicateObs, b.InstrumentID, b.TradeDate, "more than one row for this instrument-day")
		}
		seen[b.TradeDate]++

		if b.Volume < 0 || b.Value < 0 || b.TradeCount < 0 {
			b.QualityCodes = append(b.QualityCodes, QNegativeVolumeValue)
			q.add(QNegativeVolumeValue, b.InstrumentID, b.TradeDate,
				fmt.Sprintf("volume=%g value=%g count=%d", b.Volume, b.Value, b.TradeCount))
		}
		// OHLC consistency is only meaningful on a day that traded: a halted
		// row legitimately carries zero prices.
		if b.Volume > 0 {
			switch {
			case b.High < b.Low:
				b.QualityCodes = append(b.QualityCodes, QImpossibleOHLC)
				q.add(QImpossibleOHLC, b.InstrumentID, b.TradeDate, fmt.Sprintf("high %g < low %g", b.High, b.Low))
			case b.High <= 0 || b.Low <= 0:
				b.QualityCodes = append(b.QualityCodes, QImpossibleOHLC)
				q.add(QImpossibleOHLC, b.InstrumentID, b.TradeDate, "traded day with non-positive high/low")
			case b.FirstPrice > b.High || b.FirstPrice < b.Low || b.LastPrice > b.High || b.LastPrice < b.Low:
				b.QualityCodes = append(b.QualityCodes, QImpossibleOHLC)
				q.add(QImpossibleOHLC, b.InstrumentID, b.TradeDate,
					fmt.Sprintf("first %g / last %g outside [%g,%g]", b.FirstPrice, b.LastPrice, b.Low, b.High))
			}
			// The exchange's closing price may sit outside the traded range by
			// design (base-volume rule). It is recorded, never corrected, and
			// must never be used as an executable price.
			if b.RawBar().CloseOutsideTradedRange() {
				b.QualityCodes = append(b.QualityCodes, QCloseOutsideRange)
				q.add(QCloseOutsideRange, b.InstrumentID, b.TradeDate,
					fmt.Sprintf("close %g outside [%g,%g]", b.ClosingPrice, b.Low, b.High))
			}
			if tot := b.RealBuyVolume + b.LegalBuyVolume; tot > 0 && tot > b.Volume*1.0001 {
				b.QualityCodes = append(b.QualityCodes, QFlowExceedsTotal)
				q.add(QFlowExceedsTotal, b.InstrumentID, b.TradeDate,
					fmt.Sprintf("real+legal buy %g > total volume %g", tot, b.Volume))
			}
		}
		// A zero-volume row with a non-zero traded range, or a priced range on
		// a day classified as halted, means the state model and the data
		// disagree. Phase 5A: zero volume is never a price-limit lock.
		if b.Volume == 0 && (b.High > 0 || b.Low > 0) && b.TradingState == string(stateHalted) {
			b.QualityCodes = append(b.QualityCodes, QHaltMisclassified)
			q.add(QHaltMisclassified, b.InstrumentID, b.TradeDate,
				"zero volume but a non-zero traded range: halt classification unsafe")
		}
		if master != nil {
			if _, ok := master.At(b.InstrumentID, b.TradeDate); !ok {
				b.QualityCodes = append(b.QualityCodes, QUnknownInstrument)
				q.add(QUnknownInstrument, b.InstrumentID, b.TradeDate,
					"no security-master identity version covers this instrument-day")
			}
		}
		if cal != nil {
			if d, ok := cal.Day(b.TradeDate); ok && !d.IsTradingDay && b.Volume > 0 {
				q.add(QTimestampAmbiguous, b.InstrumentID, b.TradeDate,
					"trades reported on a date the calendar does not consider a trading day")
			}
		}
	}

	// Series-level gates.
	for i := 1; i < len(bars); i++ {
		prev, cur := bars[i-1], bars[i]
		// A base price that does not match the previous close is a price
		// discontinuity: a corporate action until an event source names it.
		if prev.ClosingPrice > 0 && cur.PreviousClose > 0 && cur.PreviousClose != prev.ClosingPrice {
			q.add(QActionDiscontinuity, cur.InstrumentID, cur.TradeDate,
				fmt.Sprintf("base price %g != previous close %g (ratio %.6f)",
					cur.PreviousClose, prev.ClosingPrice, cur.PreviousClose/prev.ClosingPrice))
		}
	}
	if cal != nil {
		have := map[string]bool{}
		for _, b := range bars {
			have[b.TradeDate] = true
		}
		var first, last string
		for _, b := range bars {
			if first == "" || b.TradeDate < first {
				first = b.TradeDate
			}
			if b.TradeDate > last {
				last = b.TradeDate
			}
		}
		for _, d := range cal.TradingDates() {
			if d >= first && d <= last && !have[d] {
				q.add(QMissingTradingDay, instrumentOf(bars), d,
					"verified trading day with no observation for this instrument")
			}
		}
	}
	if master != nil {
		for _, b := range bars {
			if c := master.TickerCollisions(b.TradeDate); len(c) > 0 {
				for t, ids := range c {
					q.add(QTickerCollision, "", b.TradeDate,
						fmt.Sprintf("ticker %q held by %d instruments: %v", t, len(ids), ids))
				}
				break // one report per series is enough; Counts keeps the total
			}
		}
	}
	for i := range bars {
		bars[i].QualityCodes = dedupe(bars[i].QualityCodes)
	}
	return q
}

const stateHalted = "HALTED"

func instrumentOf(bars []DailyBar) string {
	if len(bars) == 0 {
		return ""
	}
	return bars[0].InstrumentID
}

func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// CoverageOf reports the share of verified trading dates in [from,to] that have
// an observation, which is the Pattern Discovery gate's coverage metric.
func CoverageOf(bars []DailyBar, cal *TradingCalendar, from, to string) (have, expected int, ratio float64) {
	seen := map[string]bool{}
	for _, b := range bars {
		if b.TradeDate >= from && b.TradeDate <= to {
			seen[b.TradeDate] = true
		}
	}
	for _, d := range cal.TradingDates() {
		if d >= from && d <= to {
			expected++
			if seen[d] {
				have++
			}
		}
	}
	if expected == 0 {
		return have, 0, 0
	}
	return have, expected, float64(have) / float64(expected)
}
