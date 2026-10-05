package history

// Side of a simulated order.
type Side int

const (
	Buy Side = iota
	Sell
)

// Assumption labels how a fill price was obtained. Daily bars never allow an
// exact reconstruction, so no fill is ever labelled exact.
type Assumption string

const (
	AssumeOpenPrint    Assumption = "open_print"             // first trade price; a real print
	AssumeLimitTouched Assumption = "limit_touched_in_range" // limit inside [Low,High]; queue position unknown
	AssumeStopGap      Assumption = "stop_gap_adjusted"      // trigger inside range, fill at worse of trigger/open
	AssumeLockPrice    Assumption = "lock_price_queue"       // fill at lock price; queue priority unverified
	AssumeClampedLast  Assumption = "last_print_clamped"
)

type Fill struct {
	Filled     bool
	Price      float64
	Assumption Assumption
	Reason     string // set when Filled is false
}

func unfilled(reason string) Fill { return Fill{Reason: reason} }

// tradable reports whether an order on this side can be assumed fillable in
// the given state. A buy into a limit-up lock (or sell into a limit-down lock)
// sits behind a queue and is not assumed filled.
func tradable(state TradingState, side Side) (bool, string) {
	switch state {
	case StateHalted, StateSuspended:
		return false, "symbol not trading"
	case StateUnknown:
		return false, "trading state unknown"
	case StateLimitUpLocked:
		if side == Buy {
			return false, "buy behind limit-up queue"
		}
	case StateLimitDownLocked:
		if side == Sell {
			return false, "sell behind limit-down queue"
		}
	}
	return true, ""
}

func rangeOK(b RawBar) bool { return b.Traded() && b.Low > 0 && b.High >= b.Low }

// OpenFill models an at-the-open market order using the first trade print only.
// Close, last and adjusted prices are never used.
func OpenFill(b RawBar, st TradingState, side Side) Fill {
	if ok, why := tradable(st, side); !ok {
		return unfilled(why)
	}
	if !rangeOK(b) || b.Open < b.Low || b.Open > b.High {
		return unfilled("no valid open print inside traded range")
	}
	a := AssumeOpenPrint
	if st == StateLimitUpLocked || st == StateLimitDownLocked {
		a = AssumeLockPrice
	}
	return Fill{Filled: true, Price: b.Open, Assumption: a}
}

// LimitFill models a resting limit order for the day. A limit at or beyond the
// open fills at the open (price improvement); a limit inside the range fills at
// the limit; otherwise no fill.
func LimitFill(b RawBar, st TradingState, side Side, limit float64) Fill {
	if ok, why := tradable(st, side); !ok {
		return unfilled(why)
	}
	if !rangeOK(b) {
		return unfilled("no valid traded range")
	}
	if side == Buy {
		switch {
		case limit >= b.Open && b.Open >= b.Low && b.Open <= b.High:
			return Fill{true, b.Open, AssumeOpenPrint, ""}
		case limit >= b.Low:
			return Fill{true, limit, AssumeLimitTouched, ""}
		}
	} else {
		switch {
		case limit <= b.Open && b.Open >= b.Low && b.Open <= b.High:
			return Fill{true, b.Open, AssumeOpenPrint, ""}
		case limit <= b.High:
			return Fill{true, limit, AssumeLimitTouched, ""}
		}
	}
	return unfilled("limit not reached by traded range")
}

// StopFill models a stop (sell-stop for Sell, buy-stop breakout for Buy). The
// trigger is tested against trade high/low, never the closing price. If the
// day opens through the trigger the fill is at the open (gap), else at the
// trigger; both are labelled approximate.
func StopFill(b RawBar, st TradingState, side Side, trigger float64) Fill {
	if ok, why := tradable(st, side); !ok {
		return unfilled(why)
	}
	if !rangeOK(b) {
		return unfilled("no valid traded range")
	}
	if side == Sell {
		if b.Low > trigger {
			return unfilled("stop not triggered")
		}
		if b.Open <= trigger && b.Open >= b.Low {
			return Fill{true, b.Open, AssumeStopGap, ""}
		}
		return Fill{true, trigger, AssumeStopGap, ""}
	}
	if b.High < trigger {
		return unfilled("breakout not triggered")
	}
	if b.Open >= trigger && b.Open <= b.High {
		return Fill{true, b.Open, AssumeStopGap, ""}
	}
	return Fill{true, trigger, AssumeStopGap, ""}
}

// MarkPrice is the price a position may be valued at for a day's exit mark when
// no order is simulated. It is the last trade print when it lies inside the
// traded range, never the closing price when that is outside the range; if the
// last print is unusable the value is clamped into [Low,High] and labelled.
func MarkPrice(b RawBar) (price float64, a Assumption, ok bool) {
	if !rangeOK(b) {
		return 0, "", false
	}
	if b.Last >= b.Low && b.Last <= b.High && b.Last > 0 {
		return b.Last, AssumeOpenPrint, true
	}
	c := b.Close
	if c < b.Low {
		c = b.Low
	}
	if c > b.High {
		c = b.High
	}
	return c, AssumeClampedLast, true
}

// ExcursionFromEntry returns MFE and MAE (as fractions of entry, long side)
// over the bars using trade high/low only; closing prices are never read.
func ExcursionFromEntry(entry float64, bars []RawBar) (mfe, mae float64) {
	if entry <= 0 {
		return 0, 0
	}
	for _, b := range bars {
		if !rangeOK(b) {
			continue
		}
		if up := b.High/entry - 1; up > mfe {
			mfe = up
		}
		if dn := 1 - b.Low/entry; dn > mae {
			mae = dn
		}
	}
	return mfe, mae
}
