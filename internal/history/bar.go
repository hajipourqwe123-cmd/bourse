// Package history holds point-in-time daily-bar semantics: trading state,
// executable-price policy, and the separation of raw / adjusted / execution
// prices (Phase 5A). It performs no I/O.
package history

// RawBar is one daily row exactly as traded (unadjusted, rial). Field names
// follow BrsApi History.php. Zero means "no value", never "price is zero".
type RawBar struct {
	Date      string  // YYYY-MM-DD
	PrevClose float64 // py: official base price of the day
	Open      float64 // pf: first trade price
	Last      float64 // pl: last trade price
	Close     float64 // pc: closing price (exchange-defined; may not have traded)
	High      float64 // pmax: highest trade price
	Low       float64 // pmin: lowest trade price
	Volume    float64 // tvol
	Value     float64 // tval
	Count     int     // tcnt

	// Optional evidence. Zero values mean "unknown".
	UpperLimit   float64 // allowed-range ceiling for the day (feed or rules table)
	LowerLimit   float64 // allowed-range floor for the day
	SourceStatus string  // explicit status from the feed: "", "halted", "suspended"
}

// Traded reports whether any trade printed on the day.
func (b RawBar) Traded() bool { return b.Volume > 0 }

// CloseOutsideTradedRange is true when the closing price is not a price at
// which a trade could have occurred (volume-base rule; observed in 286 of 4230
// traded days for Foolad).
func (b RawBar) CloseOutsideTradedRange() bool {
	return b.Traded() && b.High > 0 && b.Low > 0 && b.Close > 0 && (b.Close < b.Low || b.Close > b.High)
}

// AdjustedBar is an analytical price series point. It is a distinct type so
// it can never be passed to the execution functions.
type AdjustedBar struct {
	Date                         string
	Open, Last, Close, High, Low float64
	Volume                       float64
	Factor                       float64 // cumulative multiplier applied to raw prices
}
