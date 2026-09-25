package model

import "time"

// Side of a flow event.
type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Band classifies an interval's average ticket per participant.
type Band string

const (
	BandHot     Band = "hot"      // >= HotThreshold  (default 200M toman)
	BandHotPlus Band = "hot_plus" // [PlusThreshold, HotThreshold) (default 100–200M toman)
	BandRetail  Band = "retail"   // below PlusThreshold
)

// Attribution tells how reliable the per-participant average is.
type Attribution string

const (
	// Attributed: the interval added new participants, so value/count is a real average.
	Attributed Attribution = "attributed"
	// Unattributed: volume grew but the (distinct) participant count did not, i.e. participants
	// already counted earlier in the day traded again. No per-participant average exists.
	Unattributed Attribution = "unattributed"
)

// FlowEvent is one detected hot-money (or hot-plus) interval for one side.
type FlowEvent struct {
	InsCode      string      `json:"ins_code"`
	Symbol       string      `json:"symbol"`
	Class        string      `json:"class"` // instrument class from the session calendar ("unknown" if unmapped)
	Side         Side        `json:"side"`
	Band         Band        `json:"band"`
	Attribution  Attribution `json:"attribution"`
	IntervalFrom time.Time   `json:"interval_from"`
	IntervalTo   time.Time   `json:"interval_to"`
	Volume       int64       `json:"volume"`       // shares in the interval (real persons, this side)
	Value        int64       `json:"value"`        // rial, Volume × interval VWAP
	Participants int64       `json:"participants"` // new distinct participants in the interval (0 when unattributed)
	AvgTicket    int64       `json:"avg_ticket"`   // rial per participant (0 when unattributed)
	VWAP         int64       `json:"vwap"`         // interval VWAP in rial
	PriceLast    int64       `json:"price_last"`
}

// QualityIssue is emitted whenever a snapshot fails a data-quality rule.
type QualityIssue struct {
	InsCode string    `json:"ins_code"`
	Code    string    `json:"code"`
	Detail  string    `json:"detail"`
	At      time.Time `json:"at"`
}

// TenMinute aggregates one instrument's flow in one 10-minute window (the "big moves" matrix).
type TenMinute struct {
	InsCode     string    `json:"ins_code"`
	Class       string    `json:"class"` // instrument class ("unknown" if unmapped)
	WindowStart time.Time `json:"window_start"`
	NetHot      int64     `json:"net_hot"`    // rial, attributed hot buy − attributed hot sell
	PriceOpen   int64     `json:"price_open"` // first last-price seen in window
	PriceLastV  int64     `json:"price_last"` // latest last-price seen in window
	// Partial is true for the window holding a late day baseline (see GameTotals.Partial): the
	// activity before that baseline is missing from NetHot. DAY_START_MISSED in docs/data-quality.md.
	Partial bool `json:"partial"`
}

// ChangePct returns the price change in percent over the window (0 when undefined).
func (t TenMinute) ChangePct() float64 {
	if t.PriceOpen <= 0 {
		return 0
	}
	return float64(t.PriceLastV-t.PriceOpen) / float64(t.PriceOpen) * 100
}

// GameTotals accumulates the "market game" split for one instrument for the day (rial, net = buy − sell).
type GameTotals struct {
	InsCode string `json:"ins_code"`
	Class   string `json:"class"` // instrument class ("unknown" if unmapped); per-class aggregates exclude unknown
	Day     string `json:"day"`
	// AsOf is the source_time, and Volume the cumulative day volume, of the latest snapshot
	// included in these totals: data age, and whether they cover a newer snapshot's trading.
	AsOf       time.Time `json:"as_of"`
	Volume     int64     `json:"volume"`
	NetHot     int64     `json:"net_hot"`
	NetHotPlus int64     `json:"net_hot_plus"`
	NetRetail  int64     `json:"net_retail"`
	// NetUnattributed holds flow that cannot be banded (existing participants trading again).
	NetUnattributed int64 `json:"net_unattributed"`
	// Partial is true unless the day's first accepted baseline was taken before the instrument's
	// own session open with zero volume: otherwise trades before it are not counted (late start,
	// data lost upstream, engine restart after the bus discarded the start of the day).
	// DAY_START_MISSED in docs/data-quality.md.
	Partial bool `json:"partial"`
}
