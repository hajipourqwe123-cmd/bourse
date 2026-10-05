package history

import (
	"math"
	"testing"
)

func TestHaltedRowIsNotLimitLock(t *testing.T) {
	b := RawBar{Date: "2026-06-01", PrevClose: 1000, Close: 1000, Volume: 0, High: 0, Low: 0}
	r := ClassifyDay(b, StateResult{}, 0, DefaultStateConfig())
	if r.State == StateLimitUpLocked || r.State == StateLimitDownLocked {
		t.Fatalf("zero-volume zero-range row classified as lock: %+v", r)
	}
	if r.State != StateHalted || r.Evidence != EvidenceInferred {
		t.Fatalf("want inferred HALTED, got %+v", r)
	}
}

func TestZeroVolumeDayClassification(t *testing.T) {
	cfg := StateConfig{SuspendedMinRun: 3}
	mk := func(vol, hi, lo, close float64) RawBar {
		return RawBar{PrevClose: 1000, Close: close, Volume: vol, High: hi, Low: lo}
	}
	bars := []RawBar{mk(500, 1010, 990, 1000), mk(0, 0, 0, 1000), mk(0, 0, 0, 1000), mk(0, 0, 0, 1000), mk(300, 1020, 1000, 1010), mk(300, 1030, 1010, 1020)}
	got := ClassifySeries(bars, cfg)
	want := []TradingState{StateTrading, StateHalted, StateHalted, StateSuspended, StateReopened, StateTrading}
	for i := range want {
		if got[i].State != want[i] {
			t.Fatalf("day %d: want %s got %+v", i, want[i], got[i])
		}
	}
	// contradictory zero-volume rows are UNKNOWN, not halted
	if r := ClassifyDay(mk(0, 1010, 0, 1000), StateResult{}, 0, cfg); r.State != StateUnknown {
		t.Fatalf("zero volume with range: %+v", r)
	}
	if r := ClassifyDay(mk(0, 0, 0, 900), StateResult{}, 0, cfg); r.State != StateUnknown {
		t.Fatalf("zero volume with changed close: %+v", r)
	}
	// explicit source status wins
	b := mk(0, 0, 0, 1000)
	b.SourceStatus = "suspended"
	if r := ClassifyDay(b, StateResult{}, 0, cfg); r.State != StateSuspended || r.Evidence != EvidenceExplicit {
		t.Fatalf("explicit: %+v", r)
	}
}

func TestLockClassification(t *testing.T) {
	up := RawBar{PrevClose: 1000, Close: 1050, Volume: 100, High: 1050, Low: 1050}
	if r := ClassifyDay(up, StateResult{}, 0, DefaultStateConfig()); r.State != StateLimitUpLocked || r.Evidence != EvidenceInferred {
		t.Fatalf("proxy up: %+v", r)
	}
	up.UpperLimit = 1050
	if r := ClassifyDay(up, StateResult{}, 0, DefaultStateConfig()); r.Evidence != EvidenceExplicit {
		t.Fatalf("known limit: %+v", r)
	}
	// single price not at the known limit is not a lock
	thin := RawBar{PrevClose: 1000, Close: 1020, Volume: 1, High: 1020, Low: 1020, UpperLimit: 1050, LowerLimit: 950}
	if r := ClassifyDay(thin, StateResult{}, 0, DefaultStateConfig()); r.State != StateTrading {
		t.Fatalf("thin single price: %+v", r)
	}
	// direction from traded price, not from a close that is outside the range
	dn := RawBar{PrevClose: 1000, Close: 1010, Volume: 100, High: 950, Low: 950}
	if r := ClassifyDay(dn, StateResult{}, 0, DefaultStateConfig()); r.State != StateLimitDownLocked {
		t.Fatalf("down lock with stray close: %+v", r)
	}
	flat := RawBar{PrevClose: 1000, Close: 1000, Volume: 100, High: 1000, Low: 1000}
	if r := ClassifyDay(flat, StateResult{}, 0, DefaultStateConfig()); r.State != StateUnknown {
		t.Fatalf("directionless single price must be UNKNOWN: %+v", r)
	}
}

func TestUnknownFallback(t *testing.T) {
	for _, b := range []RawBar{
		{Volume: 10, High: 0, Low: 0},
		{Volume: 10, High: 900, Low: 1000},
		{Volume: -1},
	} {
		if r := ClassifyDay(b, StateResult{}, 0, DefaultStateConfig()); r.State != StateUnknown || r.Evidence != EvidenceNone {
			t.Fatalf("%+v -> %+v", b, r)
		}
	}
}

// Foolad-like day: close printed outside every trade.
var outside = RawBar{Date: "d", PrevClose: 1000, Open: 1010, Last: 1020, Close: 1060, High: 1030, Low: 1000, Volume: 50}

func TestClosingPriceOutsideTradedRange(t *testing.T) {
	if !outside.CloseOutsideTradedRange() {
		t.Fatal("must flag close outside range")
	}
	p, a, ok := MarkPrice(outside)
	if !ok || p != 1020 {
		t.Fatalf("mark must be last print, got %v %v", p, a)
	}
	noLast := outside
	noLast.Last = 0
	p, a, _ = MarkPrice(noLast)
	if p != 1030 || a != AssumeClampedLast {
		t.Fatalf("clamp: %v %v", p, a)
	}
	mfe, mae := ExcursionFromEntry(1000, []RawBar{outside})
	if math.Abs(mfe-0.03) > 1e-12 || mae != 0 {
		t.Fatalf("mfe/mae must use high/low not close: %v %v", mfe, mae)
	}
}

func TestExecutablePriceSelection(t *testing.T) {
	b := RawBar{Open: 1010, Last: 1020, Close: 1060, High: 1030, Low: 990, Volume: 50}
	if f := OpenFill(b, StateTrading, Buy); !f.Filled || f.Price != 1010 || f.Assumption != AssumeOpenPrint {
		t.Fatalf("open: %+v", f)
	}
	if f := LimitFill(b, StateTrading, Buy, 1005); !f.Filled || f.Price != 1005 || f.Assumption != AssumeLimitTouched {
		t.Fatalf("limit in range: %+v", f)
	}
	if f := LimitFill(b, StateTrading, Buy, 1100); f.Price != 1010 {
		t.Fatalf("limit above open fills at open: %+v", f)
	}
	if f := LimitFill(b, StateTrading, Buy, 980); f.Filled {
		t.Fatalf("limit below range: %+v", f)
	}
	if f := StopFill(b, StateTrading, Sell, 1000); !f.Filled || f.Price != 1000 || f.Assumption != AssumeStopGap {
		t.Fatalf("stop: %+v", f)
	}
	if f := StopFill(b, StateTrading, Sell, 980); f.Filled {
		t.Fatalf("stop below low must not trigger: %+v", f)
	}
	gap := RawBar{Open: 950, Last: 960, Close: 1000, High: 970, Low: 940, Volume: 50}
	if f := StopFill(gap, StateTrading, Sell, 1000); f.Price != 950 {
		t.Fatalf("gap-down stop fills at open: %+v", f)
	}
	if f := StopFill(b, StateTrading, Buy, 1025); !f.Filled || f.Price != 1025 {
		t.Fatalf("breakout: %+v", f)
	}
	// the close must never become a fill price
	for _, f := range []Fill{OpenFill(b, StateTrading, Buy), LimitFill(b, StateTrading, Buy, 5000), StopFill(b, StateTrading, Buy, 1)} {
		if f.Price == b.Close {
			t.Fatalf("close used as fill: %+v", f)
		}
	}
}

func TestNoFillWhenNotTradable(t *testing.T) {
	b := RawBar{Open: 1050, Last: 1050, Close: 1050, High: 1050, Low: 1050, Volume: 10}
	cases := []struct {
		st   TradingState
		side Side
		fill bool
	}{
		{StateHalted, Buy, false}, {StateSuspended, Sell, false}, {StateUnknown, Buy, false},
		{StateLimitUpLocked, Buy, false}, {StateLimitUpLocked, Sell, true},
		{StateLimitDownLocked, Sell, false}, {StateLimitDownLocked, Buy, true},
	}
	for _, c := range cases {
		if f := OpenFill(b, c.st, c.side); f.Filled != c.fill {
			t.Fatalf("%s side %d: %+v", c.st, c.side, f)
		}
	}
}

func TestCorporateActionAdjustmentSeparation(t *testing.T) {
	raw := []RawBar{
		{Date: "d1", PrevClose: 1000, Open: 1000, Last: 1000, Close: 1000, High: 1010, Low: 990, Volume: 10},
		{Date: "d2", PrevClose: 500, Open: 500, Last: 505, Close: 505, High: 510, Low: 495, Volume: 20}, // 2:1 event
		{Date: "d3", PrevClose: 505, Open: 506, Last: 508, Close: 508, High: 509, Low: 505, Volume: 20},
	}
	snapshot := append([]RawBar(nil), raw...)
	ev := DetectEvents(raw)
	if len(ev) != 1 || ev[0].Date != "d2" || ev[0].Ratio != 0.5 || ev[0].Review || ev[0].Kind != ActionPriceDiscontinuity {
		t.Fatalf("events: %+v", ev)
	}
	adj := Adjust(raw)
	if adj[0].Close != 500 || adj[1].Close != 505 || adj[2].Close != 508 || adj[0].Factor != 0.5 {
		t.Fatalf("adjusted: %+v", adj)
	}
	for i := range raw {
		if raw[i] != snapshot[i] {
			t.Fatal("raw bars were mutated by adjustment")
		}
	}
	// fills use raw prices: the raw d1 open is 1000, not the adjusted 500
	if f := OpenFill(raw[0], StateTrading, Buy); f.Price != 1000 {
		t.Fatalf("fill must be raw: %+v", f)
	}
	// ratio > 1 is flagged for review; halted rows with unchanged base create no event
	up := []RawBar{{Date: "a", Close: 100}, {Date: "b", PrevClose: 100.32, Close: 101}, {Date: "c", PrevClose: 101}}
	if ev := DetectEvents(up); len(ev) != 1 || !ev[0].Review {
		t.Fatalf("review flag: %+v", ev)
	}
	halted := []RawBar{{Date: "a", Close: 100}, {Date: "b", PrevClose: 100, Close: 100}, {Date: "c", PrevClose: 100, Close: 100, Volume: 5, High: 100, Low: 100}}
	if ev := DetectEvents(halted); len(ev) != 0 {
		t.Fatalf("halted rows: %+v", ev)
	}
}
